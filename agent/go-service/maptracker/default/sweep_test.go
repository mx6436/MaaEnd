// Copyright (c) 2026 MaaEnd Contributors
package maptrackerdefault

import (
	"math"
	"testing"
)

// TestSweepPlanBounds 校验采样计划：8 个目标朝向落在 [0, 360)，且与基准角
// （0°/45°/…/315°）的偏差不超过 ±sweepJitterRangeDeg（按最短弧计算）。
func TestSweepPlanBounds(t *testing.T) {
	s := &sweepState{}
	s.reset()
	if !s.initialized {
		t.Fatal("reset should mark the plan as initialized")
	}
	if s.step != 0 {
		t.Fatalf("reset should rewind to step 0, got %d", s.step)
	}
	for i, target := range s.targets {
		if target < 0 || target >= 360 {
			t.Fatalf("target %d out of range [0, 360): %v", i, target)
		}
		delta := math.Abs(target - float64(i*45))
		if delta > 180 {
			delta = 360 - delta
		}
		if delta > sweepJitterRangeDeg {
			t.Fatalf("target %d deviates %.2f° from base %d°, exceeding ±%.1f°",
				i, delta, i*45, sweepJitterRangeDeg)
		}
	}
}

// TestSweepStateDone 校验计划耗尽语义：8 步推进后 done，reset 后回到未耗尽。
func TestSweepStateDone(t *testing.T) {
	s := &sweepState{}
	s.reset()
	for i := 0; i < len(s.targets); i++ {
		if s.done() {
			t.Fatalf("plan reported done at step %d of %d", i, len(s.targets))
		}
		s.step++
	}
	if !s.done() {
		t.Fatal("plan should report done after all steps are consumed")
	}
	s.reset()
	if s.done() {
		t.Fatal("reset should clear the done state")
	}
}

// TestNormalizeHeading 校验角度归一化到 [0, 360)。
func TestNormalizeHeading(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 0},
		{45, 45},
		{359.9, 359.9},
		{-22.5, 337.5},
		{360, 0},
		{405, 45},
		{-315, 45},
	}
	for _, c := range cases {
		got := normalizeHeading(c.in)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("normalizeHeading(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
