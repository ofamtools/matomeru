package actionitems

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ofam.dev/matomeru/store"
)

const NotifyClaimTTL = 2 * time.Minute

// NotifyOptions is a snapshot shared by the headless verb and the TUI worker.
// Since is the database-wide upgrade baseline, in Unix milliseconds.
type NotifyOptions struct {
	Enabled bool
	Sources []string
	Since   int64
}

func (o NotifyOptions) Wants(it Item, now int64) bool {
	return o.Enabled && it.Created > o.Since && it.NoticeDismissedAt == 0 && !it.Read() && it.State(now) == StateOpen && (it.Notify || slices.Contains(o.Sources, it.Source))
}

// DismissNotices records the items acknowledged in the chrome, not their read
// or archive state. A repeated clear is idempotent; another process's changes
// are preserved and a concurrently deleted item is never recreated.
func (s *Store) DismissNotices(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		now := s.now()
		for _, id := range ids {
			var it Item
			if err := tx.Get(Table.Name, id, &it); errors.Is(err, store.ErrNotFound) {
				continue
			} else if err != nil {
				return err
			}
			if it.NoticeDismissedAt != 0 {
				continue
			}
			it.NoticeDismissedAt = now
			if err := tx.Put(Table.Name, id, it); err != nil {
				return err
			}
		}
		return nil
	})
}

type notifyClaim struct {
	Item  Item
	Token string
}

// claimNotifications claims the complete batch in one transaction. Competing
// processes cannot split a burst into individual sends instead of one summary.
func (s *Store) claimNotifications(now int64, opts NotifyOptions) ([]notifyClaim, error) {
	var claims []notifyClaim
	err := s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		rows, err := tx.All(Table.Name)
		if err != nil {
			return err
		}
		for _, raw := range rows {
			var it Item
			if err := json.Unmarshal(raw, &it); err != nil {
				return err
			}
			if !opts.Wants(it, now) || it.NotifiedAt != 0 || it.NotifyClaimUntil > now {
				continue
			}
			it.NotifyClaim, it.NotifyClaimUntil = rand.Text(), now+NotifyClaimTTL.Milliseconds()
			if err := tx.Put(Table.Name, it.ID, it); err != nil {
				return err
			}
			claims = append(claims, notifyClaim{Item: it, Token: it.NotifyClaim})
		}
		return nil
	})
	return claims, err
}

func (s *Store) finishNotifications(claims []notifyClaim, now int64, sent bool) error {
	return s.db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		for _, claim := range claims {
			var it Item
			err := tx.Get(Table.Name, claim.Item.ID, &it)
			if errors.Is(err, store.ErrNotFound) {
				continue
			} // never recreate a deleted item
			if err != nil {
				return err
			}
			if it.NotifyClaim != claim.Token {
				return errors.New("action item notification claim expired or was replaced")
			}
			it.NotifyClaim, it.NotifyClaimUntil = "", 0
			if sent {
				it.NotifiedAt = now
			}
			if err := tx.Put(Table.Name, it.ID, it); err != nil {
				return err
			}
		}
		return nil
	})
}

// NotifyDue sends outside every transaction. The caller bounds the entire batch
// below NotifyClaimTTL. One successful endpoint stamps the items, even when
// another endpoint failed; total failure releases them for the next tick.
func (s *Store) NotifyDue(now int64, opts NotifyOptions, send func(title, body string) (bool, error)) (int, error) {
	claims, err := s.claimNotifications(now, opts)
	if err != nil || len(claims) == 0 {
		return 0, err
	}
	deliver := func(group []notifyClaim, title, body string) (int, error) {
		sent, err := send(title, body)
		if !sent && err == nil {
			err = errors.New("no notification sender succeeded")
		}
		err = errors.Join(err, s.finishNotifications(group, now, sent))
		if sent {
			return len(group), err
		}
		return 0, err
	}
	if len(claims) > 5 {
		return deliver(claims, "mtm inbox", fmt.Sprintf("%d new action items", len(claims)))
	}
	count := 0
	var failures []error
	for _, claim := range claims {
		it := claim.Item
		body := it.Why
		if body == "" {
			body = it.Text
		}
		if body == "" {
			body = it.URL
		}
		n, err := deliver([]notifyClaim{claim}, "mtm: "+it.Source+" · "+it.Label(), body)
		count += n
		if err != nil {
			failures = append(failures, err)
		}
	}
	return count, errors.Join(failures...)
}
