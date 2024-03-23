package imperativeapp_test

import (
	"context"
	"fmt"
	"math/rand"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Workload manages the construction of Kubernetes resources.
type Workload struct {
	Name        string
	Namespaces  []*corev1.Namespace
	Deployments []*appsv1.Deployment
	PVCs        []*corev1.PersistentVolumeClaim
	ConfigMaps  []*corev1.ConfigMap
}

// NewWorkload creates a new instance of Workload.
func NewWorkload(name string) *Workload {
	return &Workload{
		Name: name,
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("%s-namespace", name),
				},
			},
		},
	}
}

// nolint: funlen
// AddBusyBoxDeploymentWithPVC adds a deployment with an associated PVC.
func (w *Workload) AddBusyBoxDeploymentWithPVC(firstNamespace bool, storageClass string) *Workload {
	namespace := w.Namespaces[0].Name

	if !firstNamespace {
		// nolint: gosec
		namespace = w.Namespaces[rand.Intn(len(w.Namespaces))].Name
	}

	deploymentName := fmt.Sprintf("%s-busybox-deployment-%s", w.Name, unique4digitID())
	pvcName := fmt.Sprintf("%s-pvc-%s", deploymentName, unique4digitID())

	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClass,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
	w.PVCs = append(w.PVCs, pvc)

	// Create Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": deploymentName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": deploymentName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "busybox",
							Image: "quay.io/nirsof/busybox:stable",
							Args: []string{
								"sh", "-c",
								"trap exit TERM; while true; do echo $(date) | tee -a /mnt/test/outfile; sync;sleep 10 & wait; done",
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      pvcName,
									MountPath: "/mnt/test",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: pvcName,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvcName,
								},
							},
						},
					},
				},
			},
		},
	}
	w.Deployments = append(w.Deployments, deployment)

	return w
}

// AddExtraPVCs adds additional PVCs to the workload.
func (w *Workload) AddExtraPVCs(count int, firstNamespace bool, storageClass string) *Workload {
	namespace := w.Namespaces[0].Name

	if !firstNamespace {
		namespace = w.Namespaces[rand.Intn(len(w.Namespaces))].Name
	}

	for i := 0; i < count; i++ {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-extra-pvc-%d", w.Name, i),
				Namespace: namespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: &storageClass,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}
		w.PVCs = append(w.PVCs, pvc)
	}

	return w
}

// AddExtraConfigMaps adds additional ConfigMaps to the workload.
func (w *Workload) AddExtraConfigMaps(count int, firstNamespace bool) *Workload {
	namespace := w.Namespaces[0].Name

	if !firstNamespace {
		//nolint:gosec
		namespace = w.Namespaces[rand.Intn(len(w.Namespaces))].Name
	}

	data := generateRandomMapData()

	for i := 0; i < count; i++ {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-extra-config-%d", w.Name, i),
				Namespace: namespace,
			},
			Data: data,
		}
		w.ConfigMaps = append(w.ConfigMaps, configMap)
	}

	return w
}

func (w *Workload) AddExtraNamespaces(count int) *Workload {
	for i := 0; i < count; i++ {
		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("%s-namespace-%d", w.Name, i),
			},
		}
		w.Namespaces = append(w.Namespaces, namespace)
	}

	return w
}

// Create creates the workload in the cluster pointed to by the k8sClient.
func (w *Workload) Create(k8sClient client.Client) error {
	for _, ns := range w.Namespaces {
		if err := k8sClient.Create(context.TODO(), ns); err != nil {
			return err
		}
	}

	for _, pvc := range w.PVCs {
		if err := k8sClient.Create(context.TODO(), pvc); err != nil {
			return err
		}
	}

	for _, cm := range w.ConfigMaps {
		if err := k8sClient.Create(context.TODO(), cm); err != nil {
			return err
		}
	}

	for _, deploy := range w.Deployments {
		if err := k8sClient.Create(context.TODO(), deploy); err != nil {
			return err
		}
	}

	return nil
}

// deleteObject is a helper function to delete a Kubernetes object.
// It gets the object from the cluster and deletes it.
// Use k8sClient.Delete for the simpler case of just deleting it.
func deleteObject(k8sClient client.Client, obj client.Object) error {
	err := k8sClient.Get(context.TODO(), client.ObjectKey{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		return err
	}

	err = k8sClient.Delete(context.TODO(), obj)
	if err != nil {
		return err
	}

	return nil
}

func RetryOnConflict(backoff wait.Backoff, fn func(client.Client, client.Object) error,
	c client.Client, o client.Object,
) error {
	wrappedFn := func() error {
		return fn(c, o)
	}

	return retry.OnError(backoff, errors.IsConflict, wrappedFn)
}

// Delete deletes the workload from the cluster pointed to by the k8sClient.
// It will not force delete any objects.
func (w *Workload) Delete(k8sClient client.Client) error {
	for _, deploy := range w.Deployments {
		d := deploy

		err := RetryOnConflict(retry.DefaultBackoff, deleteObject, k8sClient, d)
		if err != nil {
			return err
		}
	}

	for _, cm := range w.ConfigMaps {
		c := cm

		err := RetryOnConflict(retry.DefaultBackoff, deleteObject, k8sClient, c)
		if err != nil {
			return err
		}
	}

	for _, pvc := range w.PVCs {
		p := pvc

		err := RetryOnConflict(retry.DefaultBackoff, deleteObject, k8sClient, p)
		if err != nil {
			return err
		}
	}

	for _, ns := range w.Namespaces {
		n := ns

		err := RetryOnConflict(retry.DefaultBackoff, deleteObject, k8sClient, n)
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *Workload) GetRuntimeObjects() []client.Object {
	objects := []client.Object{}
	for _, ns := range w.Namespaces {
		objects = append(objects, ns)
	}

	for _, pvc := range w.PVCs {
		objects = append(objects, pvc)
	}

	for _, cm := range w.ConfigMaps {
		objects = append(objects, cm)
	}

	for _, deploy := range w.Deployments {
		objects = append(objects, deploy)
	}

	return objects
}

func generateRandomMapData() map[string]string {
	data := map[string]string{}
	for i := 0; i < 10; i++ {
		data[fmt.Sprintf("key-%d", i)] = fmt.Sprintf("value-%s", unique4digitID())
	}

	return data
}

func unique4digitID() string {
	//nolint:gosec
	return fmt.Sprintf("%d", rand.Intn(10000))
}
