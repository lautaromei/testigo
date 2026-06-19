// Package eval computes the offline calibration metrics for testigo's audit
// detectors (AUDIT_PLAN §9). It is deterministic, dependency-free, and never
// runs in the audit runtime path — it consumes aligned
// (score_predicted, y_actual, odc_class, observable) tuples produced by the
// oracle and reports per-suite regression metrics (MAE, R²), per-mutant
// calibration metrics (Brier, ROC-AUC, precision/recall/F1), and a per-detector
// precision ranking that flags low-signal ("mierda") detectors.
package eval

import (
	"math"
	"sort"
)

// Sample is one aligned mutation site: the detector's continuous prediction
// that the site survives, the oracle's ground-truth label, and the ODC /
// observability tags used to slice the metrics.
type Sample struct {
	Rule       string  // detector name that predicted this site (empty if none)
	Score      float64 // predicted P(survive) ∈ [0,1]
	Survived   bool    // oracle label: true = mutant survived (a real gap)
	Observable bool    // boundary-observable site
	DefectType string  // ODC Defect Type, for per-class slices
}

// MAE is the mean absolute error between predicted survival probability and the
// realized label (1 for survived, 0 for killed). Lower is better.
func MAE(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += math.Abs(s.Score - label(s))
	}
	return sum / float64(len(samples))
}

// Brier is the mean squared error of the probabilistic prediction. It guards
// against a predictor that games MAE by always emitting ≈0 on the minority
// (survived) class. Lower is better.
func Brier(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		d := s.Score - label(s)
		sum += d * d
	}
	return sum / float64(len(samples))
}

// R2 is the coefficient of determination of the predicted scores against the
// realized labels, relative to the constant mean predictor. 1 is perfect, 0 is
// no better than predicting the mean, negative is worse than the mean.
func R2(samples []Sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var mean float64
	for _, s := range samples {
		mean += label(s)
	}
	mean /= float64(len(samples))

	var ssRes, ssTot float64
	for _, s := range samples {
		y := label(s)
		ssRes += (y - s.Score) * (y - s.Score)
		ssTot += (y - mean) * (y - mean)
	}
	if ssTot == 0 {
		// No variance in labels: R² is undefined; report perfect iff residuals
		// also vanish, else worst.
		if ssRes == 0 {
			return 1
		}
		return 0
	}
	return 1 - ssRes/ssTot
}

// Confusion holds the four counts at a fixed decision threshold.
type Confusion struct {
	TP, FP, TN, FN int
}

// Precision = TP / (TP+FP). Of the sites the detector flags as gaps, the
// fraction that truly survived. The headline number for ranking detectors.
func (c Confusion) Precision() float64 {
	if c.TP+c.FP == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FP)
}

// Recall = TP / (TP+FN). Of the truly-surviving sites, the fraction flagged.
func (c Confusion) Recall() float64 {
	if c.TP+c.FN == 0 {
		return 0
	}
	return float64(c.TP) / float64(c.TP+c.FN)
}

// F1 is the harmonic mean of precision and recall.
func (c Confusion) F1() float64 {
	p, r := c.Precision(), c.Recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

// ConfusionAt thresholds the scores: score >= threshold predicts "survives".
func ConfusionAt(samples []Sample, threshold float64) Confusion {
	var c Confusion
	for _, s := range samples {
		pred := s.Score >= threshold
		switch {
		case pred && s.Survived:
			c.TP++
		case pred && !s.Survived:
			c.FP++
		case !pred && s.Survived:
			c.FN++
		default:
			c.TN++
		}
	}
	return c
}

// AUC is the area under the ROC curve, computed via the Mann–Whitney U
// statistic (rank-sum), which handles ties correctly. It is the probability
// that a random surviving site scores higher than a random killed one. 0.5 is
// random; 1.0 is perfect separation. Undefined (returns 0.5) if either class is
// empty.
func AUC(samples []Sample) float64 {
	type ranked struct {
		score    float64
		survived bool
	}
	rs := make([]ranked, len(samples))
	var pos, neg int
	for i, s := range samples {
		rs[i] = ranked{s.Score, s.Survived}
		if s.Survived {
			pos++
		} else {
			neg++
		}
	}
	if pos == 0 || neg == 0 {
		return 0.5
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].score < rs[j].score })

	// Assign average ranks to ties (ranks are 1-based).
	ranks := make([]float64, len(rs))
	for i := 0; i < len(rs); {
		j := i
		for j < len(rs) && rs[j].score == rs[i].score {
			j++
		}
		avg := float64(i+1+j) / 2 // mean of ranks (i+1)..j
		for k := i; k < j; k++ {
			ranks[k] = avg
		}
		i = j
	}
	var sumPos float64
	for i, r := range rs {
		if r.survived {
			sumPos += ranks[i]
		}
	}
	u := sumPos - float64(pos)*float64(pos+1)/2
	return u / (float64(pos) * float64(neg))
}

// DetectorStat is the per-detector roll-up used to rank detectors by signal.
type DetectorStat struct {
	Rule      string
	N         int     // sites this detector fired on
	Precision float64 // fraction that truly survived
	Recall    float64 // fraction of all survivors this detector caught
	F1        float64
	MeanScore float64 // mean predicted score (calibration check vs precision)
}

// ByDetector computes per-detector precision/recall/F1 by treating every site a
// detector fired on as a positive prediction (the detector emitted a finding).
// Recall is measured against the global survivor population so detectors are
// comparable. Detectors are returned sorted ascending by precision so the
// worst ("mierda") detectors surface first.
func ByDetector(samples []Sample) []DetectorStat {
	totalSurvivors := 0
	for _, s := range samples {
		if s.Survived {
			totalSurvivors++
		}
	}
	type agg struct {
		n, tp    int
		scoreSum float64
	}
	groups := map[string]*agg{}
	for _, s := range samples {
		if s.Rule == "" {
			continue
		}
		g := groups[s.Rule]
		if g == nil {
			g = &agg{}
			groups[s.Rule] = g
		}
		g.n++
		g.scoreSum += s.Score
		if s.Survived {
			g.tp++
		}
	}
	out := make([]DetectorStat, 0, len(groups))
	for rule, g := range groups {
		prec := 0.0
		if g.n > 0 {
			prec = float64(g.tp) / float64(g.n)
		}
		rec := 0.0
		if totalSurvivors > 0 {
			rec = float64(g.tp) / float64(totalSurvivors)
		}
		f1 := 0.0
		if prec+rec > 0 {
			f1 = 2 * prec * rec / (prec + rec)
		}
		out = append(out, DetectorStat{
			Rule:      rule,
			N:         g.n,
			Precision: prec,
			Recall:    rec,
			F1:        f1,
			MeanScore: g.scoreSum / float64(g.n),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Precision != out[j].Precision {
			return out[i].Precision < out[j].Precision
		}
		return out[i].Rule < out[j].Rule
	})
	return out
}

// PerSuiteRisk is the per-suite regression prediction (AUDIT_PLAN §9.1).
type PerSuiteRisk struct {
	PredictedSurvivalRate float64 // mean predicted P(survive) over observable sites
	ActualSurvivalRate    float64 // oracle mutation survival rate over observable sites
	ObservableFraction    float64
}

// PerSuite computes the per-suite estimator restricted to boundary-observable
// sites, plus the observable fraction over all enumerated sites.
func PerSuite(samples []Sample) PerSuiteRisk {
	var obs []Sample
	for _, s := range samples {
		if s.Observable {
			obs = append(obs, s)
		}
	}
	var r PerSuiteRisk
	if len(samples) > 0 {
		r.ObservableFraction = float64(len(obs)) / float64(len(samples))
	}
	if len(obs) == 0 {
		return r
	}
	var predSum, actSum float64
	for _, s := range obs {
		predSum += s.Score
		if s.Survived {
			actSum++
		}
	}
	r.PredictedSurvivalRate = predSum / float64(len(obs))
	r.ActualSurvivalRate = actSum / float64(len(obs))
	return r
}

func label(s Sample) float64 {
	if s.Survived {
		return 1
	}
	return 0
}
