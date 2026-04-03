package reconciler

import (
	"context"
	"reflect"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// ReconcileStepDuration measures the duration of each HandleReconcile call, labelled by
// the resource kind, the previous and resulting phase and condition reason, and the outcome.
var ReconcileStepDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "aruba_reconcile_step_duration_seconds",
		Help:    "Duration in seconds of each HandleReconcile call.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"resource_kind", "previous_phase", "previous_reason", "phase", "reason", "result"},
)

// TransitionActionDuration measures the duration of each transition action execution,
// labelled by the resource kind, the transition name, the previous and resulting phase
// and condition reason, and the outcome.
var TransitionActionDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "aruba_transition_action_duration_seconds",
		Help:    "Duration in seconds of each transition action execution.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"resource_kind", "transition_name", "previous_phase", "previous_reason", "phase", "reason", "result"},
)

// TransitionUnmatchedTotal counts how many times TransitionSet.Run exhausted all
// transitions without a match, falling through to the default action.
var TransitionUnmatchedTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "aruba_transition_unmatched_total",
		Help: "Number of TransitionSet.Run calls where no transition condition matched.",
	},
	[]string{"resource_kind", "phase", "reason"},
)

func init() {
	metrics.Registry.MustRegister(ReconcileStepDuration, TransitionActionDuration, TransitionUnmatchedTotal)
}

// getResourceKind returns the Kubernetes Kind name of the given ResourceObject.
func getResourceKind(obj ResourceObject) string {
	return reflect.TypeOf(obj).Elem().Name()
}

// getPhaseAndReason returns the current phase and the reason of the active condition
// (the one with Status=True) from the given ResourceObject's status.
// Returns empty strings if the object has no status or no active condition.
func getPhaseAndReason(obj ResourceObject) (phase string, reason string) {
	rs := obj.GetResourceStatus()
	if rs == nil {
		return "", ""
	}
	phase = string(rs.Phase)
	for _, cond := range rs.Conditions {
		if cond.Status == metav1.ConditionTrue {
			reason = cond.Reason
			break
		}
	}
	return phase, reason
}

// observeStep records one histogram observation for the given ResourceObject.
// It extracts the resource kind, phase, and reason from obj, then records the elapsed
// duration since start. result is "success" when err is nil, "error" otherwise.
// previousPhase and previousReason are the phase/reason captured before HandleReconcile ran.
func observeStep(obj ResourceObject, previousPhase, previousReason string, start time.Time, err error) {
	duration := time.Since(start).Seconds()
	result := "success"
	if err != nil {
		result = "error"
	}
	kind := getResourceKind(obj)
	phase, reason := getPhaseAndReason(obj)
	ReconcileStepDuration.WithLabelValues(kind, previousPhase, previousReason, phase, reason, result).Observe(duration)
}

// captureMetrics records the reconciliation duration after HandleReconcile returns.
// It re-fetches the resource from the API server so the phase and reason labels reflect
// the status written by the reconciliation. If the resource is no longer found (e.g. it
// was deleted during reconciliation), objTemplate is used as a fallback.
// previousPhase and previousReason are the phase/reason captured before HandleReconcile ran.
func captureMetrics(ctx context.Context, reconciler *Reconciler, req controllerruntime.Request, objTemplate ResourceObject, previousPhase, previousReason string, startTs time.Time, err error) {
	if freshObj, _ := reconciler.getResource(ctx, req, objTemplate); freshObj != nil {
		observeStep(freshObj, previousPhase, previousReason, startTs, err)
		return
	}

	observeStep(objTemplate, previousPhase, previousReason, startTs, err)
}

// observeTransition records one histogram observation on TransitionActionDuration.
// previousPhase and previousReason are captured before the action ran; phase and reason
// are extracted from obj after the action returns.
func observeTransition(obj ResourceObject, transitionName, previousPhase, previousReason string, start time.Time, err error) {
	duration := time.Since(start).Seconds()
	result := "success"
	if err != nil {
		result = "error"
	}
	kind := getResourceKind(obj)
	phase, reason := getPhaseAndReason(obj)
	TransitionActionDuration.WithLabelValues(kind, transitionName, previousPhase, previousReason, phase, reason, result).Observe(duration)
}

// countUnmatchedTransition increments TransitionUnmatchedTotal for the given object's
// current resource kind, phase, and condition reason.
func countUnmatchedTransition(obj ResourceObject) {
	kind := getResourceKind(obj)
	phase, reason := getPhaseAndReason(obj)
	TransitionUnmatchedTotal.WithLabelValues(kind, phase, reason).Inc()
}
