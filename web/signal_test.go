package web

import (
	"math"
	"testing"
)

func TestAssessSignal(t *testing.T) {
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	cases := []struct {
		name string
		in   []float32
		want SignalKind
	}{
		{"меняется", []float32{0.5, 0.48, 0.52}, SignalOK},
		{"ровно ноль — как MD5", []float32{0, 0, 0, 0}, SignalNoSignal},
		{"уставка", []float32{315.588, 315.588}, SignalConst},
		{"часть мусора", []float32{1, nan, 2, 3e12}, SignalFaulty},
		{"только мусор", []float32{nan, inf}, SignalEmpty},
	}
	for _, c := range cases {
		got := assessSignal(c.in)
		if got.Kind != c.want {
			t.Errorf("%s: получили %v, ждали %v", c.name, got.Kind, c.want)
		}
	}
	if assessSignal([]float32{5, 5}).Problem() {
		t.Error("ненулевая константа (уставка) — не проблема")
	}
}

func TestPeers(t *testing.T) {
	names := []string{
		"Oildr: MD1 Casedrain Pressure", "Oildr: MD2 Casedrain Pressure", "Oildr: MD5 Casedrain Pressure",
		"hyd:service sensor 1", "hyd:service sensor 2",
	}
	sig := []Signal{
		{Kind: SignalOK}, {Kind: SignalOK}, {Kind: SignalNoSignal}, // MD5 мёртв на фоне живых соседей
		{Kind: SignalNoSignal}, {Kind: SignalNoSignal}, // сервисные датчики не используются вовсе
	}
	markPeers(names, sig)
	if !sig[2].Problem() || sig[2].Label() != "нет сигнала: 0 весь цикл, а у однотипных есть (MD1, MD2)" {
		t.Errorf("MD5: %v / %q", sig[2].Problem(), sig[2].Label())
	}
	if sig[3].Problem() || sig[4].Problem() {
		t.Error("ноль у всех однотипных — это неиспользуемые каналы, а не неисправность")
	}
}
