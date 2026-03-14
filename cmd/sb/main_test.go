package main

import "testing"

func TestFormatCreatedAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		createdAt string
		want      string
	}{
		{
			name:      "empty string",
			createdAt: "",
			want:      "",
		},
		{
			name:      "RFC3339 timestamp",
			createdAt: "2026-03-08T10:30:00Z",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "RFC3339 with timezone offset",
			createdAt: "2026-03-08T10:30:00+03:00",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "RFC3339Nano timestamp",
			createdAt: "2026-03-08T10:30:00.123456789Z",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "Docker high-precision nanoseconds",
			createdAt: "2026-03-08T10:30:00.1234567890Z",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "bare ISO timestamp without timezone",
			createdAt: "2026-03-08T10:30:00",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "short fallback returns first 16 chars",
			createdAt: "2026-03-08 10:30:00 garbage",
			want:      "2026-03-08 10:30",
		},
		{
			name:      "very short unparseable returns as-is",
			createdAt: "unknown",
			want:      "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatCreatedAt(tc.createdAt); got != tc.want {
				t.Fatalf("formatCreatedAt(%q) = %q, want %q", tc.createdAt, got, tc.want)
			}
		})
	}
}

func TestStatusText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		raw         string
		wantDisplay string
	}{
		{name: "running", raw: "running", wantDisplay: "running"},
		{name: "exited maps to stopped", raw: "exited", wantDisplay: "stopped"},
		{name: "stopped maps to stopped", raw: "stopped", wantDisplay: "stopped"},
		{name: "created maps to stopped", raw: "created", wantDisplay: "stopped"},
		{name: "empty maps to unknown", raw: "", wantDisplay: "unknown"},
		{name: "unexpected status maps to unknown", raw: "paused", wantDisplay: "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotDisplay, _ := statusText(tc.raw)
			if gotDisplay != tc.wantDisplay {
				t.Fatalf("statusText(%q) display = %q, want %q", tc.raw, gotDisplay, tc.wantDisplay)
			}
		})
	}
}

