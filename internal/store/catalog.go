package store

import (
	"context"
	"encoding/json"

	"llm-gateway/internal/config"
	"llm-gateway/internal/store/dbgen"
)

func (s *Store) SeedModels(ctx context.Context, models map[string]config.Model) error {
	q := dbgen.New(s.DB)
	for n, m := range models {
		b, e := json.Marshal(m)
		if e != nil {
			return e
		}
		if e = q.SeedModel(ctx, dbgen.SeedModelParams{Name: n, Config: b}); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Model(ctx context.Context, name string) (config.Model, error) {
	var m config.Model
	b, e := dbgen.New(s.DB).GetModel(ctx, name)
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	return m, e
}
func (s *Store) Models(ctx context.Context) (map[string]config.Model, error) {
	rows, e := dbgen.New(s.DB).ListModels(ctx)
	if e != nil {
		return nil, e
	}
	out := map[string]config.Model{}
	for _, row := range rows {
		var m config.Model
		if e = json.Unmarshal(row.Config, &m); e != nil {
			return nil, e
		}
		out[row.Name] = m
	}
	return out, nil
}
func (s *Store) SetModel(ctx context.Context, name string, m config.Model) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	return dbgen.New(s.DB).SetModel(ctx, dbgen.SetModelParams{Name: name, Config: b})
}
