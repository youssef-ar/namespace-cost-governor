package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CostReportSpec defines the desired state of CostReport
type CostReportSpec struct {
}

// CostReportStatus defines the observed state of CostReport.
type CostReportStatus struct {
	Period          string           `json:"period,omitempty"` // YYYY-MM
	NameSpace       string           `json:"namespace,omitempty"`
	TotalCost       TotalCost        `json:"totalCost,omitempty"`
	TopConsumers    []TopConsumer    `json:"topConsumers,omitempty"`
	IdleEvents      []IdleEvent      `json:"idleEvents,omitempty"`
	SuspendedEvents []SuspendedEvent `json:"suspendedEvents,omitempty"`

	// conditions represent the current state of the CostReport resource.
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

type TotalCost struct {
	Cpu    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	// based on configurable price-per-unit
	EstimatedCost string `json:"estimatedCost,omitempty"`
}

type TopConsumer struct {
	Workload      string `json:"workload,omitempty"`
	CpuPercent    int    `json:"cpuPercent,omitempty"`
	MemoryPercent int    `json:"memoryPercent,omitempty"`
}

type IdleEvent struct {
	Workload       string `json:"workload,omitempty"`
	ScaledDownAt   string `json:"scaledDownAt,omitempty"`
	EstimatedSaved string `json:"estimatedSaved,omitempty"`
}

type SuspendedEvent struct {
	Workload       string `json:"workload,omitempty"`
	SuspendedAt    string `json:"suspendedAt,omitempty"`
	EstimatedSaved string `json:"estimatedSaved,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// CostReport is the Schema for the costreports API
type CostReport struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of CostReport
	// +required
	Spec CostReportSpec `json:"spec"`

	// status defines the observed state of CostReport
	// +optional
	Status CostReportStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CostReportList contains a list of CostReport
type CostReportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CostReport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CostReport{}, &CostReportList{})
}
