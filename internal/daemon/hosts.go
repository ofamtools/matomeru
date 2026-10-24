package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

type hostKey struct{}

func WithHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, hostKey{}, host)
}
func Host(ctx context.Context) string { host, _ := ctx.Value(hostKey{}).(string); return host }

// Qualify keeps local identities unchanged and remote identities collision-free.
func Qualify(host, kind, value string) string {
	if host == "" || host == "local" || value == "" {
		return value
	}
	return "mtm-host://" + url.PathEscape(host) + "/" + kind + "/" + url.PathEscape(value)
}
func SplitIdentity(value string) (host, kind, raw string) {
	if !strings.HasPrefix(value, "mtm-host://") {
		return "", "", value
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "mtm-host://"), "/", 3)
	if len(parts) != 3 {
		return "", "", value
	}
	host, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", value
	}
	raw, err = url.PathUnescape(parts[2])
	if err != nil {
		return "", "", value
	}
	return host, parts[1], raw
}
func routeParams(ctx context.Context, params any) (string, any, error) {
	host := Host(ctx)
	if params == nil {
		return host, nil, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return "", nil, err
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return host, params, nil
	}
	for _, key := range []string{"session", "window", "window_id"} {
		var value string
		if json.Unmarshal(values[key], &value) != nil {
			continue
		}
		h, _, v := SplitIdentity(value)
		if h == "" {
			continue
		}
		if host != "" && host != "local" && host != h {
			return "", nil, fmt.Errorf("destination identities name different hosts")
		}
		host = h
		values[key], _ = json.Marshal(v)
	}
	return host, values, nil
}

type HostReport struct {
	Name   string `json:"host"`
	Addr   string `json:"addr"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}
type HostReports struct {
	Hosts []HostReport `json:"hosts"`
}
