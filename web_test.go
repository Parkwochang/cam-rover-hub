package main

import (
	"bytes"
	"testing"
)

func TestCockpitAssetsEmbedded(t *testing.T) {
	for _, id := range []string{"cockpit", "video", "minimap", "openSettings", "roverAddress", "emergencyStop"} {
		if !bytes.Contains(indexHTML, []byte(`id="`+id+`"`)) {
			t.Errorf("missing cockpit element: %s", id)
		}
	}
	if !bytes.Contains(styleCSS, []byte("safe-area-inset-bottom")) || !bytes.Contains(appJS, []byte("pointercancel")) {
		t.Fatal("missing mobile safety/layout assets")
	}
}
