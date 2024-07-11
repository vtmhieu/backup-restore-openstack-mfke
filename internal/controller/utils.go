package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

// // Create dynamic shoot kube-client from shoot kubeconfig file
// func (r *TrivyReconciler) CreateDynamicKubeClient(ctx context.Context, shootKubeconfigDataString string) (*dynamic.DynamicClient, error) {
// 	shootKubeconfigDataByte := []byte(shootKubeconfigDataString)
// 	shootKubeconfigPath := "/tmp/kubeconfig"
// 	// Write the kubeconfig data to a temporary file
// 	err := os.WriteFile(shootKubeconfigPath, shootKubeconfigDataByte, 0644)
// 	if err != nil {
// 		return nil, fmt.Errorf("unable to write kubeconfig data to temporary file: %v", err)
// 	}
// 	shootConfig, err := clientcmd.BuildConfigFromFlags("", shootKubeconfigPath)
// 	if err != nil {
// 		return nil, fmt.Errorf("unable to build shoot config from kubeconfig file: %v", err)
// 	}
// 	dynamicClient, err := dynamic.NewForConfig(shootConfig)
// 	if err != nil {
// 		fmt.Printf("Error creating dynamic client: %s\n", err)
// 		os.Exit(1)
// 	}
// 	return dynamicClient, nil
// }

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
