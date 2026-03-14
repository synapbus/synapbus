package webhooks

import (
	"net"
	"testing"
)

func TestComputeHMACSignature(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		payload string
	}{
		{name: "basic signature", secret: "my-secret", payload: `{"event":"message.received"}`},
		{name: "empty payload", secret: "key", payload: ""},
		{name: "empty secret", secret: "", payload: "data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig := ComputeHMACSignature([]byte(tt.secret), []byte(tt.payload))
			if len(sig) < 10 {
				t.Errorf("signature too short: %q", sig)
			}
			if sig[:7] != "sha256=" {
				t.Errorf("signature should start with 'sha256=', got %q", sig[:7])
			}

			// Same inputs produce same output (deterministic)
			sig2 := ComputeHMACSignature([]byte(tt.secret), []byte(tt.payload))
			if sig != sig2 {
				t.Errorf("non-deterministic: %q != %q", sig, sig2)
			}
		})
	}

	// Different secrets produce different signatures
	sig1 := ComputeHMACSignature([]byte("secret-a"), []byte("payload"))
	sig2 := ComputeHMACSignature([]byte("secret-b"), []byte("payload"))
	if sig1 == sig2 {
		t.Error("different secrets should produce different signatures")
	}

	// Different payloads produce different signatures
	sig3 := ComputeHMACSignature([]byte("key"), []byte("payload-a"))
	sig4 := ComputeHMACSignature([]byte("key"), []byte("payload-b"))
	if sig3 == sig4 {
		t.Error("different payloads should produce different signatures")
	}
}

func TestValidateWebhookURL_HTTPS(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "valid HTTPS", url: "https://example.com/webhook", wantErr: false},
		{name: "HTTPS with port", url: "https://example.com:8443/hook", wantErr: false},
		{name: "HTTPS with path", url: "https://hooks.example.com/v1/receive", wantErr: false},
		{name: "HTTP rejected when allowHTTP=false", url: "http://example.com/webhook", wantErr: true},
		{name: "FTP rejected", url: "ftp://example.com/file", wantErr: true},
		{name: "no scheme", url: "example.com/webhook", wantErr: true},
		{name: "empty URL", url: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWebhookURL(tt.url, false, true) // allowHTTP=false, allowPrivate=true
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWebhookURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateWebhookURL_HTTP_Allowed(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "HTTP allowed", url: "http://example.com/webhook", wantErr: false},
		{name: "HTTPS still works", url: "https://example.com/webhook", wantErr: false},
		{name: "FTP still rejected", url: "ftp://example.com/file", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWebhookURL(tt.url, true, true) // allowHTTP=true, allowPrivate=true
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWebhookURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateWebhookURL_PrivateIP(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "public IP allowed", url: "https://93.184.216.34/hook", wantErr: false},
		{name: "loopback blocked", url: "https://127.0.0.1/hook", wantErr: true},
		{name: "RFC1918 10.x blocked", url: "https://10.0.0.1/hook", wantErr: true},
		{name: "RFC1918 172.16.x blocked", url: "https://172.16.0.1/hook", wantErr: true},
		{name: "RFC1918 192.168.x blocked", url: "https://192.168.1.1/hook", wantErr: true},
		{name: "link-local blocked", url: "https://169.254.0.1/hook", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWebhookURL(tt.url, true, false) // allowHTTP=true, allowPrivate=false
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWebhookURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		// RFC1918
		{name: "10.0.0.0/8 start", ip: "10.0.0.1", want: true},
		{name: "10.0.0.0/8 end", ip: "10.255.255.255", want: true},
		{name: "172.16.0.0/12 start", ip: "172.16.0.1", want: true},
		{name: "172.31.255.255 end", ip: "172.31.255.255", want: true},
		{name: "172.32.0.1 not private", ip: "172.32.0.1", want: false},
		{name: "192.168.0.0/16 start", ip: "192.168.0.1", want: true},
		{name: "192.168.255.255 end", ip: "192.168.255.255", want: true},

		// Loopback
		{name: "loopback 127.0.0.1", ip: "127.0.0.1", want: true},
		{name: "loopback 127.255.255.255", ip: "127.255.255.255", want: true},

		// Link-local
		{name: "link-local 169.254.0.1", ip: "169.254.0.1", want: true},
		{name: "link-local 169.254.255.255", ip: "169.254.255.255", want: true},

		// Public IPs
		{name: "public 8.8.8.8", ip: "8.8.8.8", want: false},
		{name: "public 93.184.216.34", ip: "93.184.216.34", want: false},
		{name: "public 1.1.1.1", ip: "1.1.1.1", want: false},

		// IPv6
		{name: "IPv6 loopback", ip: "::1", want: true},
		{name: "IPv6 ULA fc00::", ip: "fc00::1", want: true},
		{name: "IPv6 ULA fd00::", ip: "fd00::1", want: true},
		{name: "IPv6 link-local fe80::", ip: "fe80::1", want: true},
		{name: "IPv6 public 2001:db8::1", ip: "2001:db8::1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tt.ip)
			}
			got := IsPrivateIP(ip)
			if got != tt.want {
				t.Errorf("IsPrivateIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestIsValidEvent(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{name: "message.received", event: "message.received", want: true},
		{name: "message.mentioned", event: "message.mentioned", want: true},
		{name: "channel.message", event: "channel.message", want: true},
		{name: "invalid event", event: "invalid.event", want: false},
		{name: "empty string", event: "", want: false},
		{name: "close but wrong", event: "message.sent", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidEvent(tt.event)
			if got != tt.want {
				t.Errorf("IsValidEvent(%q) = %v, want %v", tt.event, got, tt.want)
			}
		})
	}
}
