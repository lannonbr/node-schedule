package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeScheduleExecutionSpec controls the bounded set of Job execution settings
// exposed by NodeSchedule.
type NodeScheduleExecutionSpec struct {
	// ActiveDeadlineSeconds limits the total duration of a Job.
	// +kubebuilder:validation:Minimum=1
	// +optional
	ActiveDeadlineSeconds *int64 `json:"activeDeadlineSeconds,omitempty"`

	// BackoffLimit is the number of retries before a Job is failed.
	// +kubebuilder:validation:Minimum=0
	// +optional
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// SuccessfulJobsHistoryLimit is the number of successful Jobs to retain.
	// +kubebuilder:validation:Minimum=0
	// +optional
	SuccessfulJobsHistoryLimit *int32 `json:"successfulJobsHistoryLimit,omitempty"`

	// FailedJobsHistoryLimit is the number of failed Jobs to retain.
	// +kubebuilder:validation:Minimum=0
	// +optional
	FailedJobsHistoryLimit *int32 `json:"failedJobsHistoryLimit,omitempty"`

	// StartingDeadlineSeconds is how late a missed execution may start.
	// +kubebuilder:validation:Minimum=0
	// +optional
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`
}

// NodeScheduleSpec defines the desired state of NodeSchedule.
type NodeScheduleSpec struct {
	// Schedule is a Kubernetes CronJob schedule.
	// +kubebuilder:validation:MinLength=1
	Schedule string `json:"schedule"`

	// TimeZone is an IANA time zone. It defaults to Etc/UTC.
	// +optional
	TimeZone string `json:"timeZone,omitempty"`

	// Suspend prevents future scheduled runs.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// ScriptConfigMapRef names a ConfigMap containing the script.js key.
	ScriptConfigMapRef corev1.LocalObjectReference `json:"scriptConfigMapRef"`

	// Env supplies literal values or Secret key references to the script.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Execution configures Job deadlines, retries, and history limits.
	// +optional
	Execution NodeScheduleExecutionSpec `json:"execution,omitempty"`

	// Resources configures compute requests and limits for the Node container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// NodeScheduleStatus defines the observed state of NodeSchedule.
type NodeScheduleStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions includes the Ready condition.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	CronJobName       string `json:"cronJobName,omitempty"`
	LastRunPhase      string `json:"lastRunPhase,omitempty"`
	MostRecentJobName string `json:"mostRecentJobName,omitempty"`
	Reason            string `json:"reason,omitempty"`

	LastStartTime      *metav1.Time `json:"lastStartTime,omitempty"`
	LastCompletionTime *metav1.Time `json:"lastCompletionTime,omitempty"`
	LastSuccessTime    *metav1.Time `json:"lastSuccessTime,omitempty"`
	LastFailureTime    *metav1.Time `json:"lastFailureTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=nsched,categories=all
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Last Run",type=string,JSONPath=`.status.lastRunPhase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type NodeSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeScheduleSpec   `json:"spec,omitempty"`
	Status NodeScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NodeScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeSchedule `json:"items"`
}
