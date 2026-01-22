/*
MIT License

Copyright (c) 2025 Advanced Micro Devices, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package aimkvcache

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	aimv1alpha1 "github.com/amd-enterprise-ai/aim-engine/api/v1alpha1"
	"github.com/amd-enterprise-ai/aim-engine/internal/utils"
)

func (r *AIMKVCacheReconciler) buildRedisStatefulSet(kvc *aimv1alpha1.AIMKVCache) *appsv1.StatefulSet {
	name, err := r.GetStatefulSetName(kvc)
	if err != nil {
		return nil
	}
	serviceName, err := r.GetServiceName(kvc)
	if err != nil {
		return nil
	}
	labels := map[string]string{
		"app":                          "redis",
		"aim.eai.amd.com/kvcache":      kvc.Name,
		"aim.eai.amd.com/kvcache-type": kvc.Spec.KVCacheType,
	}

	replicas := int32(1)

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: kvc.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: serviceName,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			PodManagementPolicy: appsv1.OrderedReadyPodManagement,
			PersistentVolumeClaimRetentionPolicy: &appsv1.StatefulSetPersistentVolumeClaimRetentionPolicy{
				WhenDeleted: appsv1.DeletePersistentVolumeClaimRetentionPolicyType,
				WhenScaled:  appsv1.RetainPersistentVolumeClaimRetentionPolicyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "redis",
							Image: r.getImage(kvc),
							Command: []string{
								"redis-server",
								"--appendonly", "yes",
								"--save", "60", "1",
								"--loglevel", "notice",
							},
							Env:       r.getEnv(kvc),
							Resources: r.getResources(kvc),
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 6379,
									Name:          "redis",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "redis-data",
									MountPath: "/data",
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									TCPSocket: &corev1.TCPSocketAction{
										Port: intstr.FromInt(6379),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{"redis-cli", "ping"},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
								TimeoutSeconds:      3,
								FailureThreshold:    3,
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "redis-data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      r.getStorageAccessModes(kvc),
						StorageClassName: r.getStorageClassName(kvc),
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: r.getStorageSize(kvc),
							},
						},
					},
				},
			},
		},
	}

	return statefulSet
}

func (r *AIMKVCacheReconciler) buildRedisService(kvc *aimv1alpha1.AIMKVCache) *corev1.Service {
	name, err := r.GetServiceName(kvc)
	if err != nil {
		return nil
	}
	labels := map[string]string{
		"app":                          "redis",
		"aim.eai.amd.com/kvcache":      kvc.Name,
		"aim.eai.amd.com/kvcache-type": kvc.Spec.KVCacheType,
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: kvc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Port:       6379,
					TargetPort: intstr.FromInt(6379),
					Name:       "redis",
				},
			},
		},
	}

	return service
}

func (r *AIMKVCacheReconciler) GetStatefulSetName(kvc *aimv1alpha1.AIMKVCache) (string, error) {
	return utils.GenerateDerivedName([]string{kvc.Name, kvc.Spec.KVCacheType}, utils.WithHashSource(kvc.Namespace, kvc.Name))
}

func (r *AIMKVCacheReconciler) GetServiceName(kvc *aimv1alpha1.AIMKVCache) (string, error) {
	return utils.GenerateDerivedName([]string{kvc.Name, kvc.Spec.KVCacheType, "svc"}, utils.WithHashSource(kvc.Namespace, kvc.Name))
}

func (r *AIMKVCacheReconciler) getStorageSize(kvc *aimv1alpha1.AIMKVCache) resource.Quantity {
	if kvc.Spec.Storage != nil && kvc.Spec.Storage.Size != nil {
		return *kvc.Spec.Storage.Size
	}
	return resource.MustParse("1Gi")
}

func (r *AIMKVCacheReconciler) getStorageClassName(kvc *aimv1alpha1.AIMKVCache) *string {
	if kvc.Spec.Storage != nil && kvc.Spec.Storage.StorageClassName != nil {
		return kvc.Spec.Storage.StorageClassName
	}
	return nil
}

func (r *AIMKVCacheReconciler) getStorageAccessModes(kvc *aimv1alpha1.AIMKVCache) []corev1.PersistentVolumeAccessMode {
	if kvc.Spec.Storage != nil && len(kvc.Spec.Storage.AccessModes) > 0 {
		return kvc.Spec.Storage.AccessModes
	}
	return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
}

func (r *AIMKVCacheReconciler) getImage(kvc *aimv1alpha1.AIMKVCache) string {
	// If image is explicitly set, use it
	if kvc.Spec.Image != nil && *kvc.Spec.Image != "" {
		return *kvc.Spec.Image
	}

	// Otherwise, use defaults based on KVCacheType
	switch kvc.Spec.KVCacheType {
	case kvCacheTypeRedis:
		return kvCacheDefaultRedisImage
	default:
		// Fallback to redis if type is not recognized
		return kvCacheDefaultRedisImage
	}
}

func (r *AIMKVCacheReconciler) getEnv(kvc *aimv1alpha1.AIMKVCache) []corev1.EnvVar {
	if kvc.Spec.Env != nil {
		return kvc.Spec.Env
	}

	return nil
}

func (r *AIMKVCacheReconciler) getResources(kvc *aimv1alpha1.AIMKVCache) corev1.ResourceRequirements {
	if kvc.Spec.Resources != nil {
		return *kvc.Spec.Resources
	}

	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
	}
}
