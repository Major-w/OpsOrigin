package command

import (
	"errors"
	"testing"
)

func TestVerifyNilAllows(t *testing.T) {
	var w *Whitelist
	if err := w.Verify("rm -rf /"); err != nil {
		t.Fatalf("nil whitelist should not block: %v", err)
	}
}

func TestVerifyEmptyDenies(t *testing.T) {
	w, err := New("enforce", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Verify("ls /"); !errors.Is(err, ErrDenied) {
		t.Fatalf("empty whitelist must deny, got %v", err)
	}
}

func TestVerifyExact(t *testing.T) {
	w, _ := New("enforce", []Rule{
		{Type: RuleExact, Pattern: "ls"},
	})
	if err := w.Verify("ls -la /tmp"); err != nil {
		t.Fatalf("exact match should pass: %v", err)
	}
	if err := w.Verify("cat /etc/passwd"); !errors.Is(err, ErrDenied) {
		t.Fatalf("non-matching must deny, got %v", err)
	}
}

func TestVerifyPrefix(t *testing.T) {
	w, _ := New("enforce", []Rule{
		{Type: RulePrefix, Pattern: "/usr/bin/"},
	})
	if err := w.Verify("/usr/bin/python3 script.py"); err != nil {
		t.Fatalf("prefix match should pass: %v", err)
	}
	if err := w.Verify("/tmp/evil arg"); !errors.Is(err, ErrDenied) {
		t.Fatalf("non-prefix must deny, got %v", err)
	}
}

func TestVerifyRegex(t *testing.T) {
	w, err := New("enforce", []Rule{
		{Type: RuleRegex, Pattern: `^systemctl (start|stop|restart) [a-z0-9_-]+$`},
	})
	if err != nil {
		t.Fatalf("compile regex: %v", err)
	}
	if err := w.Verify("systemctl restart nginx"); err != nil {
		t.Fatalf("valid systemctl should pass: %v", err)
	}
	if err := w.Verify("systemctl delete everything; rm -rf /"); !errors.Is(err, ErrDenied) {
		t.Fatalf("injection must deny, got %v", err)
	}
}

func TestVerifyPath(t *testing.T) {
	w, _ := New("enforce", []Rule{
		{Type: RulePath, Pattern: "/opt/ops/scripts/deploy.sh"},
	})
	if err := w.Verify("/opt/ops/scripts/deploy.sh --env prod"); err != nil {
		t.Fatalf("path match should pass: %v", err)
	}
	if err := w.Verify("deploy.sh"); !errors.Is(err, ErrDenied) {
		t.Fatalf("relative path must deny, got %v", err)
	}
}

func TestFromJSON(t *testing.T) {
	raw := `[{"type":"exact","pattern":"uptime"},{"type":"regex","pattern":"^df -h$"}]`
	w, err := FromJSON("enforce", raw)
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if err := w.Verify("uptime"); err != nil {
		t.Fatalf("exact from json should pass: %v", err)
	}
	if err := w.Verify("df -h"); err != nil {
		t.Fatalf("regex from json should pass: %v", err)
	}
	if err := w.Verify("reboot"); !errors.Is(err, ErrDenied) {
		t.Fatalf("unlisted must deny, got %v", err)
	}
}
