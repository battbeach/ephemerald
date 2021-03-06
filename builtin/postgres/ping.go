package postgres

import (
	"encoding/json"

	"github.com/boz/ephemerald/lifecycle"
	"github.com/boz/ephemerald/params"
)

func init() {
	lifecycle.MakeActionPlugin("postgres.ping", actionPGPingParse)
}

func actionPGPingParse(buf []byte) (lifecycle.Action, error) {
	action := &actionPGPing{
		ActionConfig: lifecycle.ActionConfig{
			Retries: defaultRetries,
			Timeout: defaultTimeout,
			Delay:   defaultDelay,
		},
	}
	return action, json.Unmarshal(buf, action)
}

type actionPGPing struct {
	lifecycle.ActionConfig
}

func (a *actionPGPing) Do(e lifecycle.Env, p params.Params) error {
	db, err := openDB(e, p)
	if err != nil {
		return err
	}
	defer db.Close()
	err = db.Ping()
	if err != nil {
		e.Log().WithError(err).Debug("ERROR: ping")
	}
	return err
}
