package eval

import (
	"math"
	"testing"
)

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// Hand-computed fixture:
//
//	survived=true  -> label 1
//	survived=false -> label 0
var fixture = []Sample{
	{Rule: "a", Score: 0.9, Survived: true, Observable: true},
	{Rule: "a", Score: 0.8, Survived: false, Observable: true},
	{Rule: "b", Score: 0.2, Survived: false, Observable: false},
	{Rule: "b", Score: 0.6, Survived: true, Observable: true},
}

func TestMAE(t *testing.T) {
	// |0.9-1| + |0.8-0| + |0.2-0| + |0.6-1| = 0.1+0.8+0.2+0.4 = 1.5; /4 = 0.375
	approx(t, "MAE", MAE(fixture), 0.375)
}

func TestBrier(t *testing.T) {
	// 0.01 + 0.64 + 0.04 + 0.16 = 0.85; /4 = 0.2125
	approx(t, "Brier", Brier(fixture), 0.2125)
}

func TestR2(t *testing.T) {
	// mean label = 0.5
	// ssRes = 0.01+0.64+0.04+0.16 = 0.85
	// ssTot = 0.25*4 = 1.0
	// R2 = 1 - 0.85 = 0.15
	approx(t, "R2", R2(fixture), 0.15)
}

func TestR2NoVariance(t *testing.T) {
	all := []Sample{
		{Score: 1, Survived: true},
		{Score: 1, Survived: true},
	}
	approx(t, "R2 perfect", R2(all), 1)
	bad := []Sample{
		{Score: 0, Survived: true},
		{Score: 0, Survived: true},
	}
	approx(t, "R2 worst", R2(bad), 0)
}

func TestConfusionAt(t *testing.T) {
	// threshold 0.5: predictions true,true,false,true
	// labels:                 1,    0,    0,    1
	// TP=a(0.9,1), FP=a(0.8,0), TN=b(0.2,0), TP? b(0.6,1)=TP
	c := ConfusionAt(fixture, 0.5)
	if c.TP != 2 || c.FP != 1 || c.TN != 1 || c.FN != 0 {
		t.Fatalf("confusion = %+v", c)
	}
	approx(t, "precision", c.Precision(), 2.0/3.0)
	approx(t, "recall", c.Recall(), 1.0)
	approx(t, "f1", c.F1(), 2*(2.0/3.0)*1/((2.0/3.0)+1))
}

func TestAUCPerfect(t *testing.T) {
	s := []Sample{
		{Score: 0.9, Survived: true},
		{Score: 0.8, Survived: true},
		{Score: 0.2, Survived: false},
		{Score: 0.1, Survived: false},
	}
	approx(t, "AUC perfect", AUC(s), 1.0)
}

func TestAUCRandomTies(t *testing.T) {
	// all equal scores -> AUC 0.5 (U statistic on full ties)
	s := []Sample{
		{Score: 0.5, Survived: true},
		{Score: 0.5, Survived: false},
		{Score: 0.5, Survived: true},
		{Score: 0.5, Survived: false},
	}
	approx(t, "AUC ties", AUC(s), 0.5)
}

func TestAUCInverted(t *testing.T) {
	s := []Sample{
		{Score: 0.1, Survived: true},
		{Score: 0.9, Survived: false},
	}
	approx(t, "AUC inverted", AUC(s), 0.0)
}

func TestAUCSingleClass(t *testing.T) {
	s := []Sample{{Score: 0.5, Survived: true}}
	approx(t, "AUC single class", AUC(s), 0.5)
}

func TestByDetector(t *testing.T) {
	stats := ByDetector(fixture)
	// total survivors = 2.
	// detector a: n=2, tp=1 -> prec 0.5, recall 0.5
	// detector b: n=2, tp=1 -> prec 0.5, recall 0.5
	// tie on precision -> sorted by rule: a then b
	if len(stats) != 2 {
		t.Fatalf("want 2 detectors, got %d", len(stats))
	}
	if stats[0].Rule != "a" || stats[1].Rule != "b" {
		t.Fatalf("order = %s,%s", stats[0].Rule, stats[1].Rule)
	}
	approx(t, "a precision", stats[0].Precision, 0.5)
	approx(t, "a recall", stats[0].Recall, 0.5)
	approx(t, "a meanscore", stats[0].MeanScore, 0.85)
}

func TestByDetectorRanksWorstFirst(t *testing.T) {
	s := []Sample{
		{Rule: "good", Score: 0.7, Survived: true},
		{Rule: "good", Score: 0.7, Survived: true},
		{Rule: "mierda", Score: 0.7, Survived: false},
		{Rule: "mierda", Score: 0.7, Survived: false},
	}
	stats := ByDetector(s)
	if stats[0].Rule != "mierda" {
		t.Fatalf("worst detector should rank first, got %s", stats[0].Rule)
	}
	approx(t, "mierda precision", stats[0].Precision, 0.0)
	approx(t, "good precision", stats[1].Precision, 1.0)
}

func TestPerSuite(t *testing.T) {
	// observable sites: 0.9(1), 0.8(0), 0.6(1) -> pred mean = 2.3/3, actual = 2/3
	r := PerSuite(fixture)
	approx(t, "observable fraction", r.ObservableFraction, 0.75)
	approx(t, "predicted", r.PredictedSurvivalRate, 2.3/3.0)
	approx(t, "actual", r.ActualSurvivalRate, 2.0/3.0)
}
