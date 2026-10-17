package routes_test

import (
	"net/http"
	"testing"
)

func onLAN(r *http.Request) { r.Header.Set("X-MagicBoxie-Network", "lan") }

func lanToken(t *testing.T, s *testServer) string {
	t.Helper()
	r := s.post("/api/auth/lan", nil, noAuth, onLAN)
	if r.Status != http.StatusOK {
		t.Fatalf("LAN login: %d %s", r.Status, r.Body)
	}
	var body struct{ AccessToken string }
	r.decode(t, &body)
	return body.AccessToken
}

func TestLANNeedsNoPasswordButPublicDoes(t *testing.T) {
	s := newTestServerTrustingNetwork(t, true)

	t.Run("LAN login gets a token that works at home", func(t *testing.T) {
		token := lanToken(t, s)
		if r := s.get("/Users/Me", withHeader("Authorization", "Bearer "+token), onLAN); r.Status != http.StatusOK {
			t.Fatalf("LAN token on LAN: %d %s", r.Status, r.Body)
		}
	})

	t.Run("a LAN token is refused through the public address", func(t *testing.T) {
		token := lanToken(t, s)
		if r := s.get("/Users/Me", withHeader("Authorization", "Bearer "+token)); r.Status != http.StatusUnauthorized {
			t.Fatalf("LAN token in public: got %d, want 401", r.Status)
		}
		if r := s.get("/Users/Me", withHeader("Authorization", "Bearer "+token), withHeader("X-MagicBoxie-Network", "public")); r.Status != http.StatusUnauthorized {
			t.Fatalf("LAN token tagged public: got %d, want 401", r.Status)
		}
	})

	t.Run("LAN login is refused in public", func(t *testing.T) {
		if r := s.post("/api/auth/lan", nil, noAuth); r.Status != http.StatusForbidden {
			t.Fatalf("public LAN login: got %d, want 403", r.Status)
		}
	})

	t.Run("Jellyfin login takes any password at home only", func(t *testing.T) {
		wrong := map[string]string{"Username": "admin", "Pw": "not-the-password"}
		if r := s.post("/Users/AuthenticateByName", wrong, noAuth); r.Status != http.StatusUnauthorized {
			t.Fatalf("wrong password in public: got %d, want 401", r.Status)
		}
		r := s.post("/Users/AuthenticateByName", wrong, noAuth, onLAN)
		if r.Status != http.StatusOK {
			t.Fatalf("wrong password on LAN: %d %s", r.Status, r.Body)
		}
		var body struct{ AccessToken string }
		r.decode(t, &body)
		if r := s.get("/Users/Me", withHeader("Authorization", "Bearer "+body.AccessToken)); r.Status != http.StatusUnauthorized {
			t.Fatalf("LAN-issued Jellyfin token in public: got %d, want 401", r.Status)
		}
	})

	t.Run("the real password works in public", func(t *testing.T) {
		if r := s.get("/Users/Me"); r.Status != http.StatusOK {
			t.Fatalf("full token in public: %d %s", r.Status, r.Body)
		}
	})

	t.Run("device check-in needs a token in public only", func(t *testing.T) {
		body := map[string]string{"device_id": "pi-livingroom"}
		if r := s.post("/devices/register", body, noAuth); r.Status != http.StatusUnauthorized {
			t.Fatalf("public device check-in: got %d, want 401", r.Status)
		}
		if r := s.post("/devices/register", body, noAuth, onLAN); r.Status != http.StatusOK {
			t.Fatalf("LAN device check-in: %d %s", r.Status, r.Body)
		}
		if r := s.get("/devices/videos/"+idToyStory+"/stream", noAuth); r.Status != http.StatusUnauthorized {
			t.Fatalf("public device download: got %d, want 401", r.Status)
		}
	})
}

func TestNetworkHeaderIgnoredUnlessTrusted(t *testing.T) {
	s := newTestServer(t)
	if r := s.post("/api/auth/lan", nil, noAuth, onLAN); r.Status != http.StatusForbidden {
		t.Fatalf("LAN login without trust: got %d, want 403", r.Status)
	}
	wrong := map[string]string{"Username": "admin", "Pw": "not-the-password"}
	if r := s.post("/Users/AuthenticateByName", wrong, noAuth, onLAN); r.Status != http.StatusUnauthorized {
		t.Fatalf("wrong password claiming LAN without trust: got %d, want 401", r.Status)
	}
	if r := s.post("/devices/register", map[string]string{"device_id": "pi"}, noAuth); r.Status != http.StatusOK {
		t.Fatalf("device check-in without trust: %d %s", r.Status, r.Body)
	}
}
