package config

import "testing"

func TestIconsModeEnvPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		fileVal string
		want    string
	}{
		{name: "env overrides file value", env: "ascii", fileVal: "nerd", want: "ascii"},
		{name: "falls back to file value when env unset", env: "", fileVal: "nerd", want: "nerd"},
		{name: "empty when neither set", env: "", fileVal: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENFGA_ICONS", tt.env)
			c := &Config{Icons: tt.fileVal}
			if got := c.IconsMode(); got != tt.want {
				t.Errorf("IconsMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
