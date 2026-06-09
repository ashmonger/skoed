package controllers

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	skoedv1alpha1 "github.com/skoed/skoed-operator/api/v1alpha1"
)

const (
	raftPort          = int32(9300)
	requeueInterval   = 30 * time.Second
	apiCallTimeout    = 5 * time.Second
	certWarnDays      = 30
	certRestartAnnKey = "skoed.io/cert-restart"
)

// SkoedClusterReconciler reconciles SkoedCluster objects.
type SkoedClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *SkoedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cluster := &skoedv1alpha1.SkoedCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Apply defaults for zero-value ports.
	if cluster.Spec.API.Port == 0 {
		cluster.Spec.API.Port = 8080
	}
	if cluster.Spec.DNS.Port == 0 {
		cluster.Spec.DNS.Port = 53
	}
	if cluster.Spec.Replicas == 0 {
		cluster.Spec.Replicas = 1
	}

	for _, step := range []func(context.Context, *skoedv1alpha1.SkoedCluster) error{
		r.reconcileBootstrapSecret,
		r.reconcileService,
		r.reconcileScripts,
		r.preDeregisterIfScalingDown,
		r.reconcileStatefulSet,
		r.certRotationCheck,
	} {
		if err := step(ctx, cluster); err != nil {
			logger.Error(err, "reconcile step failed")
			return ctrl.Result{}, err
		}
	}

	if err := r.updateStatus(ctx, cluster); err != nil {
		logger.Error(err, "status update failed")
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// ── bootstrap secret ─────────────────────────────────────────────────────────

func (r *SkoedClusterReconciler) reconcileBootstrapSecret(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	name := cluster.Name + "-bootstrap"
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cluster.Namespace}, secret)
	if err == nil {
		return nil
	}
	if !kerrors.IsNotFound(err) {
		return err
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate bootstrap token: %w", err)
	}

	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cluster.Namespace,
		},
		StringData: map[string]string{
			"token": hex.EncodeToString(tokenBytes),
		},
	}
	if err := setOwnerRef(r.Scheme, cluster, secret); err != nil {
		return err
	}
	return r.Create(ctx, secret)
}

// ── headless service ─────────────────────────────────────────────────────────

func (r *SkoedClusterReconciler) reconcileService(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, svc)
	if err == nil {
		return nil
	}
	if !kerrors.IsNotFound(err) {
		return err
	}

	svc = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
			Labels:    clusterLabels(cluster.Name),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector:  clusterLabels(cluster.Name),
			Ports: []corev1.ServicePort{
				{Name: "dns-udp", Port: cluster.Spec.DNS.Port, Protocol: corev1.ProtocolUDP},
				{Name: "dns-tcp", Port: cluster.Spec.DNS.Port, Protocol: corev1.ProtocolTCP},
				{Name: "api", Port: cluster.Spec.API.Port, Protocol: corev1.ProtocolTCP},
				{Name: "raft", Port: raftPort, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	if err := setOwnerRef(r.Scheme, cluster, svc); err != nil {
		return err
	}
	return r.Create(ctx, svc)
}

// ── scripts configmap ─────────────────────────────────────────────────────────

// initConfigScript is the shell script run by the init container to write node.yaml.
// Variables are set via container env (POD_NAME, POD_NAMESPACE, API_PORT, DNS_PORT,
// RAFT_PORT, BOOTSTRAP_TOKEN). Note: ${VAR} references are shell, not Go template.
const initConfigScript = `#!/bin/sh
set -e
INDEX="${POD_NAME##*-}"
CLUSTER="${POD_NAME%-*}"
cat > /data/node.yaml <<NODEYAML
node:
  id: ${POD_NAME}
  raft_address: "0.0.0.0:${RAFT_PORT}"
  api_address: "0.0.0.0:${API_PORT}"
  dns:
    listen:
      port: ${DNS_PORT}
  data_dir: /data
NODEYAML
if [ "${INDEX}" != "0" ]; then
cat >> /data/node.yaml <<BOOTSTRAPYAML
bootstrap:
  leader_address: "${CLUSTER}-0.${CLUSTER}.${POD_NAMESPACE}.svc.cluster.local:${API_PORT}"
  token: "${BOOTSTRAP_TOKEN}"
BOOTSTRAPYAML
fi
`

func (r *SkoedClusterReconciler) reconcileScripts(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	name := cluster.Name + "-scripts"
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cluster.Namespace}, cm)
	if err == nil {
		return nil
	}
	if !kerrors.IsNotFound(err) {
		return err
	}

	cm = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cluster.Namespace,
		},
		Data: map[string]string{
			"init-config.sh": initConfigScript,
		},
	}
	if err := setOwnerRef(r.Scheme, cluster, cm); err != nil {
		return err
	}
	return r.Create(ctx, cm)
}

// ── statefulset ───────────────────────────────────────────────────────────────

func (r *SkoedClusterReconciler) reconcileStatefulSet(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, sts)
	if kerrors.IsNotFound(err) {
		desired := r.buildStatefulSet(cluster)
		if err := setOwnerRef(r.Scheme, cluster, desired); err != nil {
			return err
		}
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Only patch replicas (and image/resources) — preserve pod template annotations set by cert rotation.
	patch := client.MergeFrom(sts.DeepCopy())
	sts.Spec.Replicas = &cluster.Spec.Replicas
	sts.Spec.Template.Spec.Containers[0].Image = cluster.Spec.Image
	sts.Spec.Template.Spec.Containers[0].Resources = cluster.Spec.Resources
	return r.Patch(ctx, sts, patch)
}

func (r *SkoedClusterReconciler) buildStatefulSet(cluster *skoedv1alpha1.SkoedCluster) *appsv1.StatefulSet {
	labels := clusterLabels(cluster.Name)
	replicas := cluster.Spec.Replicas
	apiPortStr := fmt.Sprintf("%d", cluster.Spec.API.Port)
	dnsPortStr := fmt.Sprintf("%d", cluster.Spec.DNS.Port)
	raftPortStr := fmt.Sprintf("%d", raftPort)

	storageReq := corev1.VolumeResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceStorage: cluster.Spec.Storage.Size,
		},
	}
	pvcSpec := corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		Resources:   storageReq,
	}
	if cluster.Spec.Storage.StorageClass != nil {
		pvcSpec.StorageClassName = cluster.Spec.Storage.StorageClass
	}

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name,
			Namespace: cluster.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: cluster.Name,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name:    "init-config",
							Image:   "alpine:3.20",
							Command: []string{"/bin/sh", "/scripts/init-config.sh"},
							Env: []corev1.EnvVar{
								envFromField("POD_NAME", "metadata.name"),
								envFromField("POD_NAMESPACE", "metadata.namespace"),
								{Name: "API_PORT", Value: apiPortStr},
								{Name: "DNS_PORT", Value: dnsPortStr},
								{Name: "RAFT_PORT", Value: raftPortStr},
								{
									Name: "BOOTSTRAP_TOKEN",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-bootstrap"},
											Key:                  "token",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "data", MountPath: "/data"},
								{Name: "scripts", MountPath: "/scripts"},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:      "skoed",
							Image:     cluster.Spec.Image,
							Args:      []string{"--config", "/data/node.yaml"},
							Resources: cluster.Spec.Resources,
							Ports: []corev1.ContainerPort{
								{Name: "dns-udp", ContainerPort: cluster.Spec.DNS.Port, Protocol: corev1.ProtocolUDP},
								{Name: "dns-tcp", ContainerPort: cluster.Spec.DNS.Port, Protocol: corev1.ProtocolTCP},
								{Name: "api", ContainerPort: cluster.Spec.API.Port, Protocol: corev1.ProtocolTCP},
								{Name: "raft", ContainerPort: raftPort, Protocol: corev1.ProtocolTCP},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt32(cluster.Spec.API.Port),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
								FailureThreshold:    6,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "data", MountPath: "/data"},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "scripts",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: cluster.Name + "-scripts"},
									DefaultMode:          int32Ptr(0755),
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "data"},
					Spec:       pvcSpec,
				},
			},
		},
	}
}

// ── scale-down pre-deregistration ────────────────────────────────────────────

func (r *SkoedClusterReconciler) preDeregisterIfScalingDown(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, sts); err != nil {
		return client.IgnoreNotFound(err)
	}
	current := *sts.Spec.Replicas
	desired := cluster.Spec.Replicas
	if desired >= current {
		return nil
	}

	logger := log.FromContext(ctx)
	user, pass := r.adminCreds(ctx, cluster)

	// Identify current leader to handle leader-removal case.
	leaderPod := r.queryLeader(ctx, cluster, user, pass)

	for i := current - 1; i >= desired; i-- {
		podName := fmt.Sprintf("%s-%d", cluster.Name, i)

		// If this pod is the leader, transfer leadership first.
		if podName == leaderPod {
			if _, err := r.callAPI(ctx, cluster, 0, user, pass, http.MethodPost, "/api/v1/cluster/leadership/transfer", nil); err != nil {
				logger.Info("leadership transfer failed (best-effort)", "pod", podName, "err", err)
			}
		}

		// Deregister the node — auto-forwarded to current leader by skoed.
		if _, err := r.callAPI(ctx, cluster, 0, user, pass, http.MethodDelete,
			"/api/v1/cluster/nodes/"+podName, nil); err != nil {
			logger.Info("node deregistration failed (best-effort)", "pod", podName, "err", err)
		}
	}
	return nil
}

// ── cert rotation check ───────────────────────────────────────────────────────

func (r *SkoedClusterReconciler) certRotationCheck(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	if cluster.Spec.TLS == nil || cluster.Spec.TLS.SecretName == "" {
		return nil
	}

	tlsSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Spec.TLS.SecretName, Namespace: cluster.Namespace}, tlsSecret); err != nil {
		return client.IgnoreNotFound(err)
	}

	certPEM, ok := tlsSecret.Data["tls.crt"]
	if !ok {
		return nil
	}
	expiry, err := certExpiry(certPEM)
	if err != nil {
		return nil // unparseable cert — skip
	}

	// Record cert expiry in status (best-effort; updateStatus will persist it).
	t := metav1.NewTime(expiry)
	cluster.Status.CertExpiry = &t

	if time.Until(expiry) < certWarnDays*24*time.Hour {
		sts := &appsv1.StatefulSet{}
		if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, sts); err != nil {
			return client.IgnoreNotFound(err)
		}
		patch := client.MergeFrom(sts.DeepCopy())
		if sts.Spec.Template.Annotations == nil {
			sts.Spec.Template.Annotations = map[string]string{}
		}
		sts.Spec.Template.Annotations[certRestartAnnKey] = time.Now().UTC().Format(time.RFC3339)
		return r.Patch(ctx, sts, patch)
	}
	return nil
}

// ── status sync ───────────────────────────────────────────────────────────────

type clusterStatusResp struct {
	Nodes []struct {
		NodeID  string `json:"node_id"`
		Role    string `json:"role"`
		Healthy bool   `json:"healthy"`
	} `json:"nodes"`
}

func (r *SkoedClusterReconciler) updateStatus(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) error {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, sts); err != nil {
		return client.IgnoreNotFound(err)
	}
	cluster.Status.ReadyReplicas = sts.Status.ReadyReplicas

	user, pass := r.adminCreds(ctx, cluster)
	body, err := r.callAPI(ctx, cluster, 0, user, pass, http.MethodGet, "/api/v1/cluster/status", nil)

	var leader string
	var voters []string
	if err == nil {
		var resp clusterStatusResp
		if json.Unmarshal(body, &resp) == nil {
			for _, n := range resp.Nodes {
				voters = append(voters, n.NodeID)
				if n.Role == "leader" {
					leader = n.NodeID
				}
			}
		}
	}
	cluster.Status.Leader = leader
	cluster.Status.Voters = voters

	ready := cluster.Status.ReadyReplicas == cluster.Spec.Replicas
	setCondition(&cluster.Status.Conditions, "Ready", ready, "ReplicasReady", "ReplicasNotReady")
	setCondition(&cluster.Status.Conditions, "Quorum", leader != "", "LeaderElected", "NoLeader")

	return r.Status().Update(ctx, cluster)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *SkoedClusterReconciler) adminCreds(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster) (user, pass string) {
	if cluster.Spec.AdminSecretRef.Name == "" {
		return "", ""
	}
	s := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Spec.AdminSecretRef.Name, Namespace: cluster.Namespace}, s); err != nil {
		return "", ""
	}
	return string(s.Data["username"]), string(s.Data["password"])
}

func (r *SkoedClusterReconciler) queryLeader(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster, user, pass string) string {
	body, err := r.callAPI(ctx, cluster, 0, user, pass, http.MethodGet, "/api/v1/cluster/status", nil)
	if err != nil {
		return ""
	}
	var resp clusterStatusResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	for _, n := range resp.Nodes {
		if n.Role == "leader" {
			return n.NodeID
		}
	}
	return ""
}

// callAPI calls the management API on pod at ordinal podIndex and returns the response body.
func (r *SkoedClusterReconciler) callAPI(ctx context.Context, cluster *skoedv1alpha1.SkoedCluster,
	podIndex int32, user, pass, method, path string, _ []byte) ([]byte, error) {

	url := fmt.Sprintf("http://%s-%d.%s.%s.svc.cluster.local:%d%s",
		cluster.Name, podIndex, cluster.Name, cluster.Namespace, cluster.Spec.API.Port, path)

	tctx, cancel := context.WithTimeout(ctx, apiCallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(tctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API %s %s: %d", method, path, resp.StatusCode)
	}
	return body, nil
}

func setOwnerRef(scheme *runtime.Scheme, cluster *skoedv1alpha1.SkoedCluster, obj client.Object) error {
	return ctrl.SetControllerReference(cluster, obj, scheme)
}

func clusterLabels(name string) map[string]string {
	return map[string]string{
		"app":              name,
		"skoed.io/cluster": name,
	}
}

func envFromField(name, fieldPath string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: fieldPath},
		},
	}
}

func int32Ptr(v int32) *int32 { return &v }

func setCondition(conditions *[]metav1.Condition, condType string, isTrue bool, trueReason, falseReason string) {
	status := metav1.ConditionFalse
	reason := falseReason
	if isTrue {
		status = metav1.ConditionTrue
		reason = trueReason
	}
	now := metav1.Now()
	for i, c := range *conditions {
		if c.Type == condType {
			(*conditions)[i].Status = status
			(*conditions)[i].Reason = reason
			(*conditions)[i].LastTransitionTime = now
			return
		}
	}
	*conditions = append(*conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		LastTransitionTime: now,
	})
}

func certExpiry(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

// SetupWithManager registers the controller with the manager.
func (r *SkoedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&skoedv1alpha1.SkoedCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// suppress unused import (resource is used via resource.Quantity in types)
var _ = resource.MustParse
