package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"

	snapshotv1beta1 "github.com/vtmhieu/backup-restore-openstack-mfke.git/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Get Secret user-kubeconfig in ns seed
func GetSecretShootKubeconfig(ctx context.Context, c client.Client, ns string) (string, error) {
	secrets := &corev1.SecretList{}
	err := c.List(ctx, secrets, client.InNamespace(ns))
	if err != nil {
		return "", fmt.Errorf("unable to list secret in ns %s: %v", ns, err)
	}

	userKubeconfigSecretName := ""

	for _, secret := range secrets.Items {
		if strings.Contains(secret.Name, "user-kubeconfig") {
			userKubeconfigSecretName = secret.Name
		}
	}

	if userKubeconfigSecretName == "" {
		return "", fmt.Errorf("secret user-kubeconfig does not exist in ns %s: %v", ns, err)
	}

	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: userKubeconfigSecretName, Namespace: ns}
	err = c.Get(ctx, key, secret)
	if err != nil {
		return "", fmt.Errorf("unable to get secret user-kubeconfig in ns %s: %v", ns, err)
	}

	shootKubeconfigDataString := string(secret.Data["kubeconfig"])
	return shootKubeconfigDataString, nil
}

// Create shoot kube-client from shoot kubeconfig file
func CreateShootKubeClient(ctx context.Context, shootKubeconfigDataString, clustername string) (*kubernetes.Clientset, error) {
	shootKubeconfigDataByte := []byte(shootKubeconfigDataString)
	shootKubeconfigPath := os.Getenv("HOME") + "/kubeconfig/" + clustername
	err := os.MkdirAll(filepath.Dir(shootKubeconfigPath), os.ModePerm)
	if err != nil && !os.IsExist(err) {
		// b.Logger.Errorf("Could not create dir to store chart repository: %s\n", err.Error())
		return nil, err
	}
	// Write the kubeconfig data to a temporary file
	err = os.WriteFile(shootKubeconfigPath, shootKubeconfigDataByte, 0644)
	if err != nil {
		return nil, fmt.Errorf("unable to write kubeconfig data to temporary file: %v", err)
	}
	shootConfig, err := clientcmd.BuildConfigFromFlags("", shootKubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("unable to build shoot config from kubeconfig file: %v", err)
	}
	shootClientset, err := kubernetes.NewForConfig(shootConfig)
	if err != nil {
		return nil, fmt.Errorf("unable to create shoot clientset: %v", err)
	}
	return shootClientset, nil
}

// Create dynamic shoot kube-client from shoot kubeconfig file
func CreateDynamicKubeClient(ctx context.Context, shootKubeconfigDataString string) (*dynamic.DynamicClient, error) {
	shootKubeconfigDataByte := []byte(shootKubeconfigDataString)
	shootKubeconfigPath := "/tmp/kubeconfig"
	// Write the kubeconfig data to a temporary file
	err := os.WriteFile(shootKubeconfigPath, shootKubeconfigDataByte, 0644)
	if err != nil {
		return nil, fmt.Errorf("unable to write kubeconfig data to temporary file: %v", err)
	}
	shootConfig, err := clientcmd.BuildConfigFromFlags("", shootKubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("unable to build shoot config from kubeconfig file: %v", err)
	}
	dynamicClient, err := dynamic.NewForConfig(shootConfig)
	if err != nil {
		fmt.Printf("Error creating dynamic client: %s\n", err)
		os.Exit(1)
	}
	return dynamicClient, nil
}

// func get namespace in shoot
func GetNamespace(shootClientSet *kubernetes.Clientset) ([]string, error) {
	var namespaceNames []string
	namespacesList, err := shootClientSet.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}
	// Save namespaces in a slice.
	for _, ns := range namespacesList.Items {
		namespaceNames = append(namespaceNames, ns.GetName())
	}
	return namespaceNames, nil
}

func formatResourceList(resourceList corev1.ResourceList) string {
	var resources []string
	for name, quantity := range resourceList {
		resources = append(resources, fmt.Sprintf("%s: %s", name, quantity.String()))
	}
	return strings.Join(resources, ", ")
}

func convertAccessModes(accessModes []corev1.PersistentVolumeAccessMode) []string {
	var modes []string
	for _, mode := range accessModes {
		modes = append(modes, string(mode))
	}
	return modes
}

func createVolumeSnapshotClasses(dynamicClienSet *dynamic.DynamicClient) (string, error) {
	// Define the GroupVersionResource for VolumeSnapshotClass
	gvr := schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: "volumesnapshotclasses",
	}

	// Read the YAML file
	yamlFile := `
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: csi-cinder-snapclass
  annotations:
    snapshot.storage.kubernetes.io/is-default-class: "true"
driver: cinder.csi.openstack.org
deletionPolicy: Delete
parameters:
  type: Premium-SSD
  force-create: "true"
`

	// Decode the YAML file
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(yamlFile), 100)
	obj := &unstructured.Unstructured{}
	if err := decoder.Decode(obj); err != nil {
		fmt.Printf("Error decoding YAML file: %v\n", err)
		return "", err
	}

	// Apply the resource to the cluster
	namespace := "" // VolumeSnapshotClass is a cluster-scoped resource
	_, err := dynamicClienSet.Resource(gvr).Namespace(namespace).Create(context.TODO(), obj, metav1.CreateOptions{})
	if err != nil {
		fmt.Printf("Error creating resource: %v\n", err)
		return "", err
	}
	return "csi-cinder-snapclass", nil
}

func getVolumeSnapShot(dynamicClienSet *dynamic.DynamicClient,
	resourceNamespace string) ([]snapshotv1beta1.PvSnapshotItem, error) {
	pvSnapshotItemList := []snapshotv1beta1.PvSnapshotItem{}
	// // Describe the custom resource
	gvr := schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: VolumeSnapshot,
	}
	resourceClient := dynamicClienSet.Resource(gvr).Namespace(resourceNamespace)
	resourceList, err := resourceClient.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error describing custom resource: %s\n", err)
		fmt.Printf("The crds volumesnapshots is not existed in shoot cluster")
		return pvSnapshotItemList, err
	}
	for _, item := range resourceList.Items {
		pvSnapshot := snapshotv1beta1.PvSnapshotItem{}
		// get metadata time create
		metadata, found, err := unstructured.NestedMap(item.Object, "metadata")
		if err != nil || !found {
			fmt.Println("Error accessing metadata:", err)
			continue
		}
		// get name of snapshot
		snapshotName, found, err := unstructured.NestedString(metadata, "name")
		if err != nil || !found {
			fmt.Println("Error accessing metadata name:", err)
			continue
		}
		pvSnapshot.SnapshotName = snapshotName
		// get namespace of snapshot
		namespace, found, err := unstructured.NestedString(metadata, "namespace")
		if err != nil || !found {
			fmt.Println("Error accessing namespace:", err)
			continue
		}
		pvSnapshot.Namespace = namespace
		// get pvc name
		//get spec
		spec, found, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil || !found {
			fmt.Println("Error accessing spec:", err)
			continue
		}
		source, found, err := unstructured.NestedMap(spec, "source")
		if err != nil || !found {
			fmt.Println("Error accessing spec source:", err)
			continue
		} else {
			pvcName, found, err := unstructured.NestedString(source, "persistentVolumeClaimName")
			if err != nil || !found {
				fmt.Println("Error accessing spec source:", err)
				continue
			}
			pvSnapshot.PersistenVolumeClaimName = pvcName
		}
		// get volumeSnapshotClassName
		volumeSnapshotClassName, found, err := unstructured.NestedString(spec, "volumeSnapshotClassName")
		if err != nil || !found {
			fmt.Println("Error accessing spec volumeSnapshotClassName:", err)
			pvSnapshot.VolumeSnapshotClassName = Unknown
		} else {
			pvSnapshot.VolumeSnapshotClassName = volumeSnapshotClassName
		}

		// get status
		status, found, err := unstructured.NestedMap(item.Object, "status")
		if err != nil || !found {
			fmt.Println("Error accessing status:", err)
			continue
		}
		// get snapshotContent
		snapshotContent, found, err := unstructured.NestedString(status, "boundVolumeSnapshotContentName")
		if err != nil || !found {
			fmt.Println("Error accessing status snapshotContent:", err)
			pvSnapshot.BoundVolumeSnapshotContentName = Unknown
		} else {
			pvSnapshot.BoundVolumeSnapshotContentName = snapshotContent
		}
		// get creation time
		creationTimestamp, found, err := unstructured.NestedString(status, "creationTime")
		if err != nil || !found {
			fmt.Println("Error accessing metadata creationTimestamp:", err)
			pvSnapshot.CreationTime = Unknown
		} else {
			pvSnapshot.CreationTime = creationTimestamp
		}

		// get readyToUse
		readyToUse, found, err := unstructured.NestedBool(status, "readyToUse")
		if err != nil || !found {
			fmt.Println("Error accessing status readyToUse:", err)
			pvSnapshot.ReadyToUse = false
		} else {
			pvSnapshot.ReadyToUse = readyToUse
		}

		// get restoreSize
		restoreSize, found, err := unstructured.NestedString(status, "restoreSize")
		if err != nil || !found {
			fmt.Println("Error accessing status restoreSize:", err)
			pvSnapshot.RestoreSize = Unknown
		} else {
			pvSnapshot.RestoreSize = restoreSize
		}

		// add to return list
		pvSnapshotItemList = append(pvSnapshotItemList, pvSnapshot)
	}
	return pvSnapshotItemList, nil
}
