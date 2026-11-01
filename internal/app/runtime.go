package app

import (
	"context"

	"llm-gateway/internal/capture"
	"llm-gateway/internal/config"
	"llm-gateway/internal/quota"
	"llm-gateway/internal/store"
)

type Runtime struct {
	Store   *store.Store
	Quota   *quota.Limiter
	Archive capture.Archive
}

func Open(ctx context.Context, c config.Config) (*Runtime, error) {
	cipher, e := capture.NewCipher(c.EncryptionKey)
	if e != nil {
		return nil, e
	}
	s, e := store.New(ctx, c.DatabaseURL, cipher)
	if e != nil {
		return nil, e
	}
	q, e := quota.New(c.RedisURL)
	if e != nil {
		s.DB.Close()
		return nil, e
	}
	r := &Runtime{Store: s, Quota: q}
	if e = q.Client.Ping(ctx).Err(); e != nil {
		r.Close()
		return nil, e
	}
	archive, e := capture.NewArchive(c.Capture)
	if e != nil {
		r.Close()
		return nil, e
	}
	r.Archive = archive
	if e = s.SeedModels(ctx, c.Models); e != nil {
		r.Close()
		return nil, e
	}
	return r, nil
}
func (r *Runtime) Close() { r.Quota.Client.Close(); r.Store.DB.Close() }
