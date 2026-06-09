package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(&SkoedCluster{}, &SkoedClusterList{})
}

// SkoedClusterSpec defines the desired state of a skoed cluster.
type SkoedClusterSpec struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas,omitempty"`

	// Image is the skoed container image (e.g. ghcr.io/skoed/skoed:latest).
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// Storage configures the PVC template for each pod's data directory.
	Storage StorageSpec `json:"storage"`

	// DNS configures the DNS listener.
	// +kubebuilder:default={"port":53}
	DNS DNSSpec `json:"dns,omitempty"`

	// API configures the management API listener.
	// +kubebuilder:default={"port":8080}
	API APISpec `json:"api,omitempty"`

	// TLS configures optional TLS for DoH/DoT listeners.
	TLS *TLSSpec `json:"tls,omitempty"`

	// AdminSecretRef references a K8s Secret with keys "username" and "password"
	// used by the operator to call the skoed management API.
	AdminSecretRef corev1.LocalObjectReference `json:"adminSecretRef,omitempty"`

	// Resources sets CPU/memory limits on the skoed container.
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// StorageSpec configures the PVC template for each pod.
type StorageSpec struct {
	// Size is the PVC size (e.g. "1Gi").
	// +kubebuilder:validation:Required
	Size resource.Quantity `json:"size"`

	// StorageClass overrides the cluster default StorageClass.
	StorageClass *string `json:"storageClass,omitempty"`
}

// DNSSpec configures the DNS listener port.
type DNSSpec struct {
	// +kubebuilder:default=53
	Port int32 `json:"port,omitempty"`
}

// APISpec configures the management API listener port.
type APISpec struct {
	// +kubebuilder:default=8080
	Port int32 `json:"port,omitempty"`
}

// TLSSpec references an existing TLS Secret or an ACME domain.
type TLSSpec struct {
	// SecretName is the name of a K8s TLS Secret (tls.crt / tls.key).
	SecretName string `json:"secretName,omitempty"`

	// ACMEDomain enables ACME cert management inside pods for this domain.
	ACMEDomain string `json:"acmeDomain,omitempty"`
}

// SkoedClusterStatus records the observed state of a SkoedCluster.
type SkoedClusterStatus struct {
	// Conditions lists Ready and Quorum status conditions.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Leader is the pod name of the current Raft leader.
	Leader string `json:"leader,omitempty"`

	// Voters lists the pod names of all Raft voters.
	Voters []string `json:"voters,omitempty"`

	// ReadyReplicas is the number of pods currently in the Ready state.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// CertExpiry is the TLS certificate expiry date (when TLS is configured).
	CertExpiry *metav1.Time `json:"certExpiry,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sc
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=".spec.replicas"
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=".status.readyReplicas"
// +kubebuilder:printcolumn:name="Leader",type=string,JSONPath=".status.leader"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SkoedCluster is the Schema for the skoedclusters API.
type SkoedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SkoedClusterSpec   `json:"spec,omitempty"`
	Status SkoedClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SkoedClusterList contains a list of SkoedCluster.
type SkoedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SkoedCluster `json:"items"`
}

func (in *SkoedCluster) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *SkoedCluster) DeepCopy() *SkoedCluster {
	if in == nil {
		return nil
	}
	out := new(SkoedCluster)
	in.DeepCopyInto(out)
	return out
}

func (in *SkoedCluster) DeepCopyInto(out *SkoedCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *SkoedClusterList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *SkoedClusterList) DeepCopy() *SkoedClusterList {
	if in == nil {
		return nil
	}
	out := new(SkoedClusterList)
	in.DeepCopyInto(out)
	return out
}

func (in *SkoedClusterList) DeepCopyInto(out *SkoedClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]SkoedCluster, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *SkoedClusterSpec) DeepCopyInto(out *SkoedClusterSpec) {
	*out = *in
	in.Storage.DeepCopyInto(&out.Storage)
	out.DNS = in.DNS
	out.API = in.API
	if in.TLS != nil {
		out.TLS = new(TLSSpec)
		*out.TLS = *in.TLS
	}
	out.AdminSecretRef = in.AdminSecretRef
	in.Resources.DeepCopyInto(&out.Resources)
}

func (in *StorageSpec) DeepCopyInto(out *StorageSpec) {
	*out = *in
	out.Size = in.Size.DeepCopy()
	if in.StorageClass != nil {
		s := *in.StorageClass
		out.StorageClass = &s
	}
}

func (in *SkoedClusterStatus) DeepCopyInto(out *SkoedClusterStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		copy(out.Conditions, in.Conditions)
	}
	if in.Voters != nil {
		out.Voters = make([]string, len(in.Voters))
		copy(out.Voters, in.Voters)
	}
	if in.CertExpiry != nil {
		t := *in.CertExpiry
		out.CertExpiry = &t
	}
}
