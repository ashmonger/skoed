package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(&SkoedNode{}, &SkoedNodeList{})
}

// SkoedNodeStatus records the observed Raft state of a single skoed pod.
type SkoedNodeStatus struct {
	// PodName is the name of the corresponding StatefulSet pod.
	PodName string `json:"podName,omitempty"`

	// Role is the Raft role of this node.
	// +kubebuilder:validation:Enum=leader;follower;candidate;unknown
	Role string `json:"role,omitempty"`

	// Healthy is true when the node responded to the last status probe.
	Healthy bool `json:"healthy"`

	// CommitIndex is the last committed Raft log index reported by this node.
	CommitIndex int64 `json:"commitIndex,omitempty"`

	// LastContact is when the operator last successfully contacted this node.
	LastContact *metav1.Time `json:"lastContact,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sn
// +kubebuilder:printcolumn:name="Pod",type=string,JSONPath=".status.podName"
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=".status.role"
// +kubebuilder:printcolumn:name="Healthy",type=boolean,JSONPath=".status.healthy"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SkoedNode is auto-created by the operator for each StatefulSet pod.
// It is read-only from the user perspective.
type SkoedNode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status SkoedNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SkoedNodeList contains a list of SkoedNode.
type SkoedNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SkoedNode `json:"items"`
}

func (in *SkoedNode) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *SkoedNode) DeepCopy() *SkoedNode {
	if in == nil {
		return nil
	}
	out := new(SkoedNode)
	in.DeepCopyInto(out)
	return out
}

func (in *SkoedNode) DeepCopyInto(out *SkoedNode) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *SkoedNodeList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *SkoedNodeList) DeepCopy() *SkoedNodeList {
	if in == nil {
		return nil
	}
	out := new(SkoedNodeList)
	in.DeepCopyInto(out)
	return out
}

func (in *SkoedNodeList) DeepCopyInto(out *SkoedNodeList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]SkoedNode, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *SkoedNodeStatus) DeepCopyInto(out *SkoedNodeStatus) {
	*out = *in
	if in.LastContact != nil {
		t := *in.LastContact
		out.LastContact = &t
	}
}
