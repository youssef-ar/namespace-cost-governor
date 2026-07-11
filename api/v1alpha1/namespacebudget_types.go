package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespaceBudgetSpec defines the desired state of NamespaceBudget
type NamespaceBudgetSpec struct {
	Monthly       Usage         `json:"monthly"`
	IdleThreshold IdleThreshold `json:"idleThreshold"`
	Actions       Actions       `json:"actions"`
	Exclusions    []Exclusion   `json:"exclusions,omitempty"`
}

type Usage struct {
	// core-hours per month
	Cpu string `json:"cpu"`
	// Gib-hours per month
	Memory string `json:"memory"`
}
type IdleThreshold struct {
	// below this = idle
	CpuPercent int `json:"cpuPercent"`
	// averaged over this window
	Window string `json:"window"`
}
type Actions struct {
	// 80% of budget used
	OnWarning []Action `json:"onWarning,omitempty"`
	// 100% of budget used
	OnExceeded []Action `json:"onExceeded,omitempty"`
	// 120% of budget used
	OnHardLimit []Action `json:"onHardLimit,omitempty"`
}
type Action struct {
	// Notify Slack
	Notify string `json:"notify,omitempty"` //slack
	// Scale down idle workloads
	ScaleDownIdle bool `json:"scaleDownIdle,omitempty"`
	// Suspend all workloads
	SuspendAll bool `json:"suspendAll,omitempty"`
}

type Exclusion struct {
	// never touch this deployment, statefulset, or cronjob
	Name string `json:"name"`
}

// NamespaceBudgetStatus defines the observed state of NamespaceBudget.
type NamespaceBudgetStatus struct {
	Phase         string         `json:"phase,omitempty"` // OK | Warning | Exceeded | Suspended
	CurrentUsage  Usage          `json:"currentUsage,omitempty"`
	BudgetPercent int            `json:"budgetPercent,omitempty"`
	IdleWorkloads []IdleWorkload `json:"idleWorkloads,omitempty"`
	// Reference to the latest generated cost report.
	LastReportRef string `json:"lastReportRef,omitempty"`

	// conditions represent the current state of the NamespaceBudget resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
type IdleWorkload struct {
	Name      string `json:"name"`
	IdleSince string `json:"idleSince"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NamespaceBudget is the Schema for the namespacebudgets API
type NamespaceBudget struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NamespaceBudget
	// +required
	Spec NamespaceBudgetSpec `json:"spec"`

	// status defines the observed state of NamespaceBudget
	// +optional
	Status NamespaceBudgetStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NamespaceBudgetList contains a list of NamespaceBudget
type NamespaceBudgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NamespaceBudget `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NamespaceBudget{}, &NamespaceBudgetList{})
}
