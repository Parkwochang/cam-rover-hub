package rover

import "testing"

func TestNormalizeAddress(t *testing.T) {
	for input, want := range map[string]string{"cam-rover.local": "http://cam-rover.local", " CAM-ROVER.local ": "http://cam-rover.local", "172.30.1.22": "http://172.30.1.22", "http://192.168.71.1:80/": "http://192.168.71.1", "10.0.0.2:8080": "http://10.0.0.2:8080"} {
		got, err := NormalizeAddress(input)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "https://cam-rover.local", "127.0.0.1", "169.254.169.254", "8.8.8.8", "example.com", "http://user:secret@192.168.1.2", "http://192.168.1.2/api", "http://cam-rover.local?q=x", "cam-rover.local:0", "cam-rover.local:", "-bad.local", "[::1]", "172.30.1.22#secret"} {
		if _, err := NormalizeAddress(input); err == nil {
			t.Fatalf("accepted unsafe address %q", input)
		}
	}
}

func TestAddressChangeSignalsStreamRestart(t *testing.T) {
	c := New("cam-rover.local", "test-secret")
	before := c.Changed()
	c.SetAddress("192.168.71.1")
	select {
	case <-before:
	default:
		t.Fatal("old stream was not invalidated")
	}
	next := c.Changed()
	c.SetAddress("192.168.71.1")
	select {
	case <-next:
		t.Fatal("unchanged address reset stream")
	default:
	}
}
