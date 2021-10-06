package circuitbreaker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConseqSuccessRecoverer_Test(t *testing.T) {
	type call struct {
		failure bool
		want    bool
	}
	tests := []struct {
		name      string
		threshold int32
		seq       []call
	}{
		{
			name:      "threshold NOT reached",
			threshold: 3,
			seq: []call{
				{failure: false, want: false},
				{failure: false, want: false},
			},
		},
		{
			name:      "threshold reached",
			threshold: 3,
			seq: []call{
				{failure: false, want: false},
				{failure: false, want: false},
				{failure: false, want: true},
			},
		},
		{
			name:      "threshold reset and reached",
			threshold: 2,
			seq: []call{
				{failure: false, want: false},
				{failure: true, want: false},
				{failure: false, want: false},
				{failure: false, want: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RecovererConseqSuccess{
				Threshold: tt.threshold,
			}
			for _, c := range tt.seq {
				assert.Equal(t, c.want, r.Test(c.failure))
			}
		})
	}
}

func TestConseqSuccessRecoverer_Reset(t *testing.T) {
	cs := &RecovererConseqSuccess{sucesses: 1}
	cs.Reset()
	assert.Equal(t, int32(0), cs.sucesses)
}
