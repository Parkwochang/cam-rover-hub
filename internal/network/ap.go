package network

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type APConnector struct {
	Profile   string
	Interface string
}

func (a APConnector) run(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "nmcli", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("NetworkManager: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (a APConnector) Connect() error {
	return a.run("connection", "up", "id", a.Profile, "ifname", a.Interface)
}

func (a APConnector) Disconnect() error {
	return a.run("connection", "down", "id", a.Profile)
}
