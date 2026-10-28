package push

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestValidateSubscriptionHosts(t *testing.T) {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	p256dh := base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	strict := &Sender{}

	cases := map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":            true,
		"https://updates.push.services.mozilla.com/wpush/v2": true,
		"https://web.push.apple.com/abc":                     true,
		"https://db5p.notify.windows.com/w/?token=x":         true,
		"http://fcm.googleapis.com/fcm/send/abc":             false,
		"https://evil.example/fcm.googleapis.com":            false,
		"https://fcm.googleapis.com.evil.example/x":          false,
		"https://169.254.169.254/latest/meta-data":           false,
		"https://localhost/x":                                false,
	}
	for endpoint, ok := range cases {
		err := strict.ValidateSubscription(endpoint, p256dh, auth)
		if (err == nil) != ok {
			t.Errorf("%s: err = %v, want ok=%v", endpoint, err, ok)
		}
	}

	if err := strict.ValidateSubscription("https://fcm.googleapis.com/x", p256dh, "short"); err == nil {
		t.Error("short auth secret accepted")
	}
	relaxed := &Sender{allowInsecure: true}
	if err := relaxed.ValidateSubscription("http://127.0.0.1:9999/push", p256dh, auth); err != nil {
		t.Errorf("relaxed mode rejected local endpoint: %v", err)
	}
}
