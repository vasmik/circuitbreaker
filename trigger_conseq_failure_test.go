package circuitbreaker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConseqFailureTrigger_Test(t *testing.T) {
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
				{failure: true, want: false},
				{failure: true, want: false},
			},
		},
		{
			name:      "threshold reached",
			threshold: 3,
			seq: []call{
				{failure: true, want: false},
				{failure: true, want: false},
				{failure: true, want: true},
			},
		},
		{
			name:      "threshold reset and reached",
			threshold: 2,
			seq: []call{
				{failure: true, want: false},
				{failure: false, want: false},
				{failure: true, want: false},
				{failure: true, want: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cf := &TriggerConseqFailure{
				Threshold: tt.threshold,
			}
			for _, c := range tt.seq {
				assert.Equal(t, c.want, cf.Test(c.failure))
			}
		})
	}
}

func TestConseqFailureTrigger_Reset(t *testing.T) {
	cf := &TriggerConseqFailure{failures: 1}
	cf.Reset()
	assert.Equal(t, int32(0), cf.failures)
}
