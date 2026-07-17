package agentmgr

import (
	"testing"
)

func TestParseAddressList(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "comma separated ips",
			input: "192.168.1.10, 192.168.1.11",
			want:  []string{"192.168.1.10", "192.168.1.11"},
		},
		{
			name:  "newline and semicolon separated",
			input: "192.168.1.10\n192.168.1.11;192.168.1.12",
			want:  []string{"192.168.1.10", "192.168.1.11", "192.168.1.12"},
		},
		{
			name:    "invalid address",
			input:   "192.168.1.999",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAddressList(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d addresses, got %d", len(tt.want), len(got))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("expected %s at %d, got %s", tt.want[i], i, got[i])
				}
			}
		})
	}
}
