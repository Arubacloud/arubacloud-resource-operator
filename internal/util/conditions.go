package util

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func UpdateConditions(conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string) []metav1.Condition {
	now := metav1.NewTime(time.Now())

	for i, condition := range conditions {
		if condition.Type == conditionType {
			if condition.Status != status || condition.Reason != reason {
				conditions[i].Status = status
				conditions[i].Reason = reason
				conditions[i].Message = message
				conditions[i].LastTransitionTime = now
			}
			return conditions
		}
	}

	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}

	return append(conditions, newCondition)
}
