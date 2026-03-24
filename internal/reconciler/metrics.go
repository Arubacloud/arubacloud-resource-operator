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
// the resource kind, the outcome, and the resulting phase and condition reason.
var ReconcileStepDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "aruba_reconcile_step_duration_seconds",
		Help:    "Duration in seconds of each HandleReconcile call.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"resource_kind", "result", "phase", "reason"},
)

func init() {
	metrics.Registry.MustRegister(ReconcileStepDuration)
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
func observeStep(obj ResourceObject, start time.Time, err error) {
	duration := time.Since(start).Seconds()
	result := "success"
	if err != nil {
		result = "error"
	}
	kind := getResourceKind(obj)
	phase, reason := getPhaseAndReason(obj)
	ReconcileStepDuration.WithLabelValues(kind, result, phase, reason).Observe(duration)
}

// captureMetrics records the reconciliation duration after HandleReconcile returns.
// It re-fetches the resource from the API server so the phase and reason labels reflect
// the status written by the reconciliation. If the resource is no longer found (e.g. it
// was deleted during reconciliation), objTemplate is used as a fallback.
func captureMetrics(ctx context.Context, reconciler *Reconciler, req controllerruntime.Request, objTemplate ResourceObject, startTs time.Time, err error) {
	if freshObj, _ := reconciler.getResource(ctx, req, objTemplate); freshObj != nil {
		observeStep(freshObj, startTs, err)
		return
	}

	observeStep(objTemplate, startTs, err)
}
