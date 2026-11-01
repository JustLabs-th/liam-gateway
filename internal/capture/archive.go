package capture

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"llm-gateway/internal/config"
)

type Archive interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}
type Files struct{ Dir string }

func NewArchive(c config.Capture) (Archive, error) {
	if c.Storage == "filesystem" {
		if e := os.MkdirAll(c.Directory, 0700); e != nil {
			return nil, e
		}
		return &Files{c.Directory}, nil
	}
	client, e := minio.New(c.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: c.Secure})
	if e != nil {
		return nil, e
	}
	return &S3{client, c.Bucket}, nil
}
func (f *Files) path(k string) (string, error) {
	if k == "" || strings.ContainsAny(k, "/\\") || strings.Contains(k, "..") {
		return "", errors.New("invalid archive key")
	}
	return filepath.Join(f.Dir, k), nil
}
func (f *Files) Put(ctx context.Context, k string, b []byte) error {
	p, e := f.path(k)
	if e != nil {
		return e
	}
	tmp, e := os.CreateTemp(f.Dir, ".archive-*")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, e = tmp.Write(b); e != nil {
		return e
	}
	if e = tmp.Sync(); e != nil {
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	if e = os.Rename(tmp.Name(), p); e != nil {
		return e
	}
	d, e := os.Open(f.Dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (f *Files) Get(ctx context.Context, k string) ([]byte, error) {
	p, e := f.path(k)
	if e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (f *Files) Delete(ctx context.Context, k string) error {
	p, e := f.path(k)
	if e != nil {
		return e
	}
	e = os.Remove(p)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}

type S3 struct {
	Client *minio.Client
	Bucket string
}

func (s *S3) Put(ctx context.Context, k string, b []byte) error {
	_, e := s.Client.PutObject(ctx, s.Bucket, k, bytes.NewReader(b), int64(len(b)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return e
}
func (s *S3) Get(ctx context.Context, k string) ([]byte, error) {
	o, e := s.Client.GetObject(ctx, s.Bucket, k, minio.GetObjectOptions{})
	if e != nil {
		return nil, e
	}
	defer o.Close()
	return io.ReadAll(o)
}
func (s *S3) Delete(ctx context.Context, k string) error {
	return s.Client.RemoveObject(ctx, s.Bucket, k, minio.RemoveObjectOptions{})
}
