package main

import "ofam.dev/ochazuke"

// defineDocForms gives argument-free collection creators a form even when all
// their props are optional. Required props already use the framework's missing-
// argument form. Supplied arguments and every headless call retain their API.
func (a *App) defineDocForms() {
	for _, op := range a.r.Operations.Entries() {
		if !op.Creates || op.Standard == "" || op.AppHandler == nil {
			continue
		}
		copiesProps := false
		for _, p := range op.Parameters {
			copiesProps = copiesProps || p.Covers
		}
		if !copiesProps {
			continue
		}
		run := op.AppHandler
		op.AppHandler = func(c ochazuke.Ctx) ochazuke.Result {
			if a.cli || a.headless || len(c.Args) != 0 {
				return run(c)
			}
			_, err := a.r.OpenArgsForm(ochazuke.Request{Noun: op.Noun(), Verb: op.Name()}, func(q ochazuke.Request) error {
				_, err := a.r.Execute(q)
				return err
			})
			return resultError(err)
		}
		a.r.Operations.Set(op)
	}
}
