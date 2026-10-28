package calls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrRoomNotFound = errors.New("room not found")

type Config struct {
	URL       string
	APIURL    string
	APIKey    string
	APISecret string
}

type LiveKit struct {
	url    string
	apiURL string
	key    string
	secret []byte
	client *http.Client
	now    func() time.Time
}

type VideoGrant struct {
	Room           string `json:"room,omitempty"`
	RoomJoin       bool   `json:"roomJoin,omitempty"`
	RoomAdmin      bool   `json:"roomAdmin,omitempty"`
	CanPublish     *bool  `json:"canPublish,omitempty"`
	CanSubscribe   *bool  `json:"canSubscribe,omitempty"`
	CanPublishData *bool  `json:"canPublishData,omitempty"`
}

type claims struct {
	jwt.RegisteredClaims
	Name  string     `json:"name,omitempty"`
	Video VideoGrant `json:"video"`
}

func New(cfg Config) *LiveKit {
	if cfg.URL == "" {
		return nil
	}
	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = cfg.URL
		apiURL = strings.Replace(apiURL, "wss://", "https://", 1)
		apiURL = strings.Replace(apiURL, "ws://", "http://", 1)
	}
	return &LiveKit{
		url:    cfg.URL,
		apiURL: strings.TrimRight(apiURL, "/"),
		key:    cfg.APIKey,
		secret: []byte(cfg.APISecret),
		client: &http.Client{Timeout: 5 * time.Second},
		now:    time.Now,
	}
}

func (l *LiveKit) URL() string { return l.url }

func (l *LiveKit) sign(subject, name string, grant VideoGrant, ttl time.Duration) (string, error) {
	now := l.now()
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    l.key,
			Subject:   subject,
			ID:        subject,
			NotBefore: jwt.NewNumericDate(now.Add(-10 * time.Second)),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Name:  name,
		Video: grant,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(l.secret)
}

func (l *LiveKit) JoinToken(room, identity, name string) (string, error) {
	yes := true
	return l.sign(identity, name, VideoGrant{
		Room: room, RoomJoin: true,
		CanPublish: &yes, CanSubscribe: &yes, CanPublishData: &yes,
	}, 2*time.Hour)
}

func (l *LiveKit) ParseJoinToken(token string) (VideoGrant, string, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		return l.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(l.key))
	return c.Video, c.Subject, err
}

func (l *LiveKit) twirp(ctx context.Context, method, room string, body any, out any) error {
	token, err := l.sign("connecta-server", "", VideoGrant{Room: room, RoomAdmin: true}, time.Minute)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.apiURL+"/twirp/livekit.RoomService/"+method, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := l.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrRoomNotFound
	}
	if res.StatusCode != http.StatusOK {
		var te struct {
			Code string `json:"code"`
			Msg  string `json:"msg"`
		}
		_ = json.Unmarshal(data, &te)
		if te.Code == "not_found" {
			return ErrRoomNotFound
		}
		return fmt.Errorf("livekit %s: %d %s", method, res.StatusCode, te.Msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (l *LiveKit) Participants(ctx context.Context, room string) ([]string, error) {
	var out struct {
		Participants []struct {
			Identity string `json:"identity"`
		} `json:"participants"`
	}
	err := l.twirp(ctx, "ListParticipants", room, map[string]string{"room": room}, &out)
	if errors.Is(err, ErrRoomNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Participants))
	for _, p := range out.Participants {
		ids = append(ids, p.Identity)
	}
	return ids, nil
}

func (l *LiveKit) CloseRoom(ctx context.Context, room string) error {
	err := l.twirp(ctx, "DeleteRoom", room, map[string]string{"room": room}, nil)
	if errors.Is(err, ErrRoomNotFound) {
		return nil
	}
	return err
}
