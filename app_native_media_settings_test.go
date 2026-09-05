package main

import "testing"

func TestValidateNativeMediaPresentationSettings(t *testing.T) {
	tests := []struct {
		property string
		allowed  []string
		rejected []string
	}{
		{"video-aspect-override", []string{"no", "16:9", "4:3"}, []string{"yes", "1", "-2", "0", "16/9", "16:9\x00"}},
		{"video-unscaled", []string{"no", "downscale-big"}, []string{"yes", "true", "downscale"}},
		{"panscan", []string{"0", "0.5", "1"}, []string{"-0.1", "1.1", "NaN", "Inf", "-Inf", "many"}},
		{"sub-font-size", []string{"20", "38", "72"}, []string{"19.99", "72.01", "NaN", "+Inf", "-Inf", "large"}},
		{"sub-color", []string{"#FFFFFF", "#ffcc00", "#00AbCd"}, []string{"white", "#FFF", "#FFFFFFFF", "#GG0000", "FFFFFF", "#FFFFFF\n", "#FFFFFF\x00"}},
		{"sub-outline-size", []string{"0", "1.65", "6"}, []string{"-0.01", "6.01", "NaN", "Inf", "-Inf", "thin"}},
		{"sub-back-color", []string{"#AF000000", "#00000000", "#aF123aBc"}, []string{"black", "#000000", "#ZZ000000", "AF000000", "#AF000000\n"}},
		{"sub-border-style", []string{"background-box", "outline-and-shadow"}, []string{"opaque-box", "none", "1"}},
		{"sub-ass-override", []string{"force", "scale"}, []string{"yes", "no", "strip", "true"}},
	}
	for _, tt := range tests {
		t.Run(tt.property, func(t *testing.T) {
			for _, value := range tt.allowed {
				if err := validateNativeMediaCommand([]string{"set", tt.property, value}); err != nil {
					t.Errorf("value %q should be accepted: %v", value, err)
				}
				if err := validateNativeMediaCommand([]string{"set", tt.property, value, "extra"}); err == nil {
					t.Errorf("extra argument with value %q should be rejected", value)
				}
			}
			for _, value := range append(tt.rejected, "", " ") {
				if err := validateNativeMediaCommand([]string{"set", tt.property, value}); err == nil {
					t.Errorf("value %q should be rejected", value)
				}
			}
			if err := validateNativeMediaCommand([]string{"set", tt.property}); err == nil {
				t.Error("missing value should be rejected")
			}
		})
	}
	for _, property := range []string{"vf", "sub-file", "sub-font", "sub-scale", "video-aspect-method", "script-opts"} {
		if err := validateNativeMediaCommand([]string{"set", property, "1"}); err == nil {
			t.Errorf("unapproved property %q should be rejected", property)
		}
	}
}
