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

// CreateSandboxRequest describes a new image-backed sandbox.
type CreateSandboxRequest struct {
	Image          string
	Entrypoint     []string
	Timeout        time.Duration
	ResourceLimits map[string]string
	Metadata       map[string]string
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
	Image          apiSandboxImage   `json:"image"`
	Entrypoint     []string          `json:"entrypoint"`
	Timeout        int               `json:"timeout"`
	ResourceLimits map[string]string `json:"resourceLimits"`
	Metadata       map[string]string `json:"metadata,omitempty"`
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
	Containers []containerSpec `json:"containers"`
}

type containerSpec struct {
	Image   string   `json:"image"`
	Command []string `json:"command"`
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
