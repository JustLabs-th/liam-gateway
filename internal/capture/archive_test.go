package capture

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"llm-gateway/internal/config"
)

func TestS3ArchiveRoundTrip(t *testing.T) {
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("unsigned S3 request")
		}
		if r.URL.Query().Has("location") {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PUT":
			var body io.Reader = r.Body
			if strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked") || r.Header.Get("X-Amz-Decoded-Content-Length") != "" {
				body = httputil.NewChunkedReader(r.Body)
			}
			b, e := io.ReadAll(body)
			if e != nil {
				t.Error(e)
				http.Error(w, "bad body", 400)
				return
			}
			objects[r.URL.Path] = b
			w.Header().Set("ETag", `"mock-etag"`)
			w.WriteHeader(200)
		case "GET":
			b, ok := objects[r.URL.Path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			w.Header().Set("ETag", `"mock-etag"`)
			_, _ = w.Write(b)
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		}
	}))
	defer server.Close()
	a, e := NewArchive(config.Capture{Storage: "s3", Endpoint: strings.TrimPrefix(server.URL, "http://"), Bucket: "captures", AccessKey: "test-access", SecretKey: "test-secret"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	b := []byte("encrypted archive")
	if e = a.Put(ctx, "request.json.enc", b); e != nil {
		t.Fatal(e)
	}
	out, e := a.Get(ctx, "request.json.enc")
	if e != nil || !bytes.Equal(out, b) {
		t.Fatalf("round trip %q %v", out, e)
	}
	if e = a.Delete(ctx, "request.json.enc"); e != nil {
		t.Fatal(e)
	}
}
func TestFilesystemRejectsTraversal(t *testing.T) {
	f := Files{Dir: t.TempDir()}
	for _, key := range []string{"../secrets", "/tmp/secrets", "a/b", "a\\b", ""} {
		if e := f.Put(context.Background(), key, []byte("x")); e == nil {
			t.Fatalf("accepted archive path %q", key)
		}
	}
}
