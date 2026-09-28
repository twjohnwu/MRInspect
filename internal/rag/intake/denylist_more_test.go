package intake

import "testing"

func TestDenylist_AdditionalKeyAndKeystoreFormats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{name: "id_ed25519", want: true},
		{name: "id_ed25519.pub", want: true},
		{name: "id_ecdsa", want: true},
		{name: "id_dsa", want: true},
		{name: "cert.p12", want: true},
		{name: "cert.pfx", want: true},
		{name: "app.jks", want: true},
		{name: "app.keystore", want: true},
		{name: "readme.md", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsDenylisted(tt.name); got != tt.want {
				t.Errorf("IsDenylisted(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}
