package validate

import (
	"strings"
	"testing"
)

func TestName(t *testing.T) {
	valid := []string{"a", "A1", "deploy_web-01", "x_y-z", strings.Repeat("a", 64)}
	for _, v := range valid {
		if err := Name("名称", v); err != nil {
			t.Errorf("Name(%q) unexpected error: %v", v, err)
		}
	}
	invalid := []string{"", "has space", "中文名", "a.b", "a/b", "a@b", "a!b", "a#b", "a$b",
		"a(b)", "a\tb", "a\nb", strings.Repeat("a", 65)}
	for _, v := range invalid {
		if err := Name("名称", v); err == nil {
			t.Errorf("Name(%q) expected error, got nil", v)
		}
	}
}

func TestHostName(t *testing.T) {
	valid := []string{"web-01.prod.local", "MasonMacBook-Pro.local", "host_1"}
	for _, v := range valid {
		if err := HostName("主机名", v); err != nil {
			t.Errorf("HostName(%q) unexpected error: %v", v, err)
		}
	}
	invalid := []string{"", "has space", "a/b", "中文", strings.Repeat("a", 65)}
	for _, v := range invalid {
		if err := HostName("主机名", v); err == nil {
			t.Errorf("HostName(%q) expected error, got nil", v)
		}
	}
}

func TestOptional(t *testing.T) {
	if err := NameOptional("名称", ""); err != nil {
		t.Errorf("NameOptional empty should pass: %v", err)
	}
	if err := NameOptional("名称", "bad name"); err == nil {
		t.Error("NameOptional invalid should fail")
	}
	if err := HostNameOptional("主机名", ""); err != nil {
		t.Errorf("HostNameOptional empty should pass: %v", err)
	}
}
