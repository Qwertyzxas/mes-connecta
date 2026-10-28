package files

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
	Region    string
}

type Storage struct {
	client *minio.Client
	bucket string
}

func Open(ctx context.Context, cfg Config) (*Storage, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, err
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, err
		}
	}
	return &Storage{client: client, bucket: cfg.Bucket}, nil
}

func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

type Object interface {
	io.ReadSeeker
	io.Closer
}

func (s *Storage) Get(ctx context.Context, key string) (Object, time.Time, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, time.Time{}, err
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, time.Time{}, err
	}
	return obj, info.LastModified, nil
}

func (s *Storage) Remove(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

var ErrBadSignature = errors.New("bad file signature")

type Signer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

func NewSigner(secret []byte, ttl time.Duration) *Signer {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("connecta-files-v1"))
	return &Signer{key: mac.Sum(nil), ttl: ttl, now: time.Now}
}

func (s *Signer) sign(id string, exp int64) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(id + "." + strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Signer) URL(id string) string {
	day := int64(24 * time.Hour / time.Second)
	exp := (s.now().Add(s.ttl).Unix()/day + 1) * day
	return "/api/files/" + id + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + s.sign(id, exp)
}

func (s *Signer) Verify(id, expRaw, sig string) error {
	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil || s.now().Unix() > exp {
		return ErrBadSignature
	}
	if !hmac.Equal([]byte(sig), []byte(s.sign(id, exp))) {
		return ErrBadSignature
	}
	return nil
}
