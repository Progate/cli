package browseros

import "testing"

func TestResultOf(t *testing.T) {
	tests := []struct {
		name string
		raw  int64
		want int64
	}{
		{name: "zero", raw: 0, want: 0},
		{name: "a pid", raw: 42, want: 42},
		{name: "an fd", raw: 0x7fffffff, want: 0x7fffffff},
		{name: "-ENOENT in the low 32 bits", raw: 0xfffffffe, want: -2},
		{name: "-EACCES in the low 32 bits", raw: 0xfffffff3, want: -13},
		{name: "a success with signals queued", raw: 1<<32 | 3, want: 3},
		{name: "a failure with signals queued", raw: 1<<32 | 0xfffffffc, want: -4},
		{name: "-errno sign-extended", raw: -2, want: -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resultOf(tt.raw); got != tt.want {
				t.Errorf("resultOf(%#x) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}
