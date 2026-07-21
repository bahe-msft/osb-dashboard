package opensandbox

import "time"

const (
	// SourceLifecycle identifies a sandbox returned by the lifecycle API.
	SourceLifecycle = "lifecycle"
	// SourceBatchSandbox identifies a BatchSandbox custom resource.
	SourceBatchSandbox = "batchsandbox"
	// SourceAgentSandbox identifies an agents.x-k8s.io Sandbox custom resource.
	SourceAgentSandbox = "agentsandbox"
)

// Sandbox is the merged lifecycle and Kubernetes discovery data needed by
// dashboard consumers.
type Sandbox struct {
	ID        string
	State     string
	CreatedAt time.Time
	Namespace string
	PodName   string
	Image     string
	CPU       string
	Memory    string
	Metadata  map[string]string
	Sources   []string
	Resources []ResourceReference
}

// ResourceReference identifies a Kubernetes custom resource backing a sandbox.
type ResourceReference struct {
	Source    string
	Group     string
	Version   string
	Plural    string
	Namespace string
	Name      string
}

// CreateSandboxRequest describes an image-backed or snapshot-restored sandbox.
type CreateSandboxRequest struct {
	Image          string
	SnapshotID     string
	Entrypoint     []string
	Timeout        time.Duration
	ResourceLimits map[string]string
	Metadata       map[string]string
}

// Snapshot is a persistent point-in-time capture managed by the lifecycle API.
type Snapshot struct {
	ID               string
	SandboxID        string
	Name             string
	State            string
	Reason           string
	Message          string
	CreatedAt        time.Time
	LastTransitionAt time.Time
}

// SandboxNodeLoad describes sandbox workloads scheduled to a Kubernetes node.
type SandboxNodeLoad struct {
	Name                   string
	SandboxCount           int
	CPURequestedMilli      int64
	CPUAllocatableMilli    int64
	MemoryRequestedBytes   int64
	MemoryAllocatableBytes int64
}

type listResponse struct {
	Items []apiSandbox `json:"items"`
}

type apiSandbox struct {
	ID        string            `json:"id"`
	Status    apiSandboxStatus  `json:"status"`
	CreatedAt time.Time         `json:"createdAt"`
	Image     apiSandboxImage   `json:"image"`
	Metadata  map[string]string `json:"metadata"`
}

type apiSandboxStatus struct {
	State string `json:"state"`
}

type apiSandboxImage struct {
	URI string `json:"uri"`
}

type apiCreateRequest struct {
	Image          *apiSandboxImage  `json:"image,omitempty"`
	SnapshotID     string            `json:"snapshotId,omitempty"`
	Entrypoint     []string          `json:"entrypoint,omitempty"`
	Timeout        *int              `json:"timeout,omitempty"`
	ResourceLimits map[string]string `json:"resourceLimits"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

type snapshotListResponse struct {
	Items      []apiSnapshot  `json:"items"`
	Pagination paginationInfo `json:"pagination"`
}

type paginationInfo struct {
	Page        int  `json:"page"`
	HasNextPage bool `json:"hasNextPage"`
}

type apiSnapshot struct {
	ID        string            `json:"id"`
	SandboxID string            `json:"sandboxId"`
	Name      string            `json:"name"`
	Status    apiSnapshotStatus `json:"status"`
	CreatedAt time.Time         `json:"createdAt"`
}

type apiSnapshotStatus struct {
	State            string    `json:"state"`
	Reason           string    `json:"reason"`
	Message          string    `json:"message"`
	LastTransitionAt time.Time `json:"lastTransitionAt"`
}

type apiCreateSnapshotRequest struct {
	Name string `json:"name,omitempty"`
}

type customResourceList struct {
	Items []sandboxResource `json:"items"`
}

type sandboxResource struct {
	Metadata resourceMetadata `json:"metadata"`
	Spec     resourceSpec     `json:"spec"`
	Status   resourceStatus   `json:"status"`
}

type resourceMetadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	Labels            map[string]string `json:"labels"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
}

type resourceSpec struct {
	Template    podTemplate `json:"template"`
	PodTemplate podTemplate `json:"podTemplate"`
}

type podTemplate struct {
	Spec podSpec `json:"spec"`
}

type podSpec struct {
	NodeName   string          `json:"nodeName"`
	Containers []containerSpec `json:"containers"`
}

type containerSpec struct {
	Image     string             `json:"image"`
	Command   []string           `json:"command"`
	Resources containerResources `json:"resources"`
}

type containerResources struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

type resourceStatus struct {
	Phase      string              `json:"phase"`
	Ready      int                 `json:"ready"`
	Selector   string              `json:"selector"`
	Conditions []resourceCondition `json:"conditions"`
}

type resourceCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type podList struct {
	Items []podResource `json:"items"`
}

type podResource struct {
	Metadata podMetadata `json:"metadata"`
	Spec     podSpec     `json:"spec"`
	Status   podStatus   `json:"status"`
}

type podMetadata struct {
	Name            string            `json:"name"`
	Labels          map[string]string `json:"labels"`
	OwnerReferences []ownerReference  `json:"ownerReferences"`
}

type ownerReference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type podStatus struct {
	Phase string `json:"phase"`
}

type nodeList struct {
	Items []nodeResource `json:"items"`
}

type nodeResource struct {
	Metadata nodeMetadata `json:"metadata"`
	Status   nodeStatus   `json:"status"`
}

type nodeMetadata struct {
	Name string `json:"name"`
}

type nodeStatus struct {
	Allocatable map[string]string `json:"allocatable"`
}

func (sandbox apiSandbox) model() Sandbox {
	return Sandbox{
		ID:        sandbox.ID,
		State:     sandbox.Status.State,
		CreatedAt: sandbox.CreatedAt,
		Image:     sandbox.Image.URI,
		Metadata:  sandbox.Metadata,
		Sources:   []string{SourceLifecycle},
	}
}

func (snapshot apiSnapshot) model() Snapshot {
	return Snapshot{
		ID:               snapshot.ID,
		SandboxID:        snapshot.SandboxID,
		Name:             snapshot.Name,
		State:            snapshot.Status.State,
		Reason:           snapshot.Status.Reason,
		Message:          snapshot.Status.Message,
		CreatedAt:        snapshot.CreatedAt,
		LastTransitionAt: snapshot.Status.LastTransitionAt,
	}
}
