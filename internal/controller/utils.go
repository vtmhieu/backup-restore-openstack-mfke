package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"

	"github.com/robfig/cron"
	snapshotv1beta1 "gitlab.fci.vn/xplat/fke/backup-restore-openstack-mfke.git/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	sevenDays         = 7 * 24 * time.Hour
	nextScheduleDelta = 100 * time.Millisecond
)

// GetSecretShootKubeconfigKey returns the namespaced name of the secret
// containing the shoot kubeconfig that corresponds to the given seed namespace.
func GetSecretShootKubeconfigKey(ctx context.Context, c client.Client, ns string) (types.NamespacedName, error) {
	kubeconfigKey := types.NamespacedName{
		Namespace: ns,
	}

	secrets := &corev1.SecretList{}
	if err := c.List(ctx, secrets, client.InNamespace(ns)); err != nil {
		return kubeconfigKey, fmt.Errorf("unable to list secret in ns %s: %v", ns, err)
	}

	userKubeconfigSecretIx := slices.IndexFunc(secrets.Items, func(secret corev1.Secret) bool {
		return strings.Contains(secret.Name, "user-kubeconfig")
	})
	if userKubeconfigSecretIx == -1 {
		return kubeconfigKey, fmt.Errorf("secret user-kubeconfig does not exist in ns %s", ns)
	}

	kubeconfigKey.Name = secrets.Items[userKubeconfigSecretIx].Name
	return kubeconfigKey, nil
}

// GetSecretShootKubeconfigObject returns the secret object containing the shoot kubeconfig
// that corresponds to the given seed namespace.
func GetSecretShootKubeconfigObject(ctx context.Context, c client.Client, ns string) (*corev1.Secret, error) {
	key, err := GetSecretShootKubeconfigKey(ctx, c, ns)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{}
	if err := c.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("unable to get secret user-kubeconfig in ns %s: %v", ns, err)
	}

	return secret, nil
}

// GetSecretShootKubeconfig returns the raw shoot kubeconfig that corresponds to
// the given seed namespace.
func GetSecretShootKubeconfig(ctx context.Context, c client.Client, ns string) (string, error) {
	secret, err := GetSecretShootKubeconfigObject(ctx, c, ns)
	if err != nil {
		return "", err
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

func checkVolumeSnapshotClasses(dynamicClientSet *dynamic.DynamicClient) (bool, error) {
	volumeSnapshotClassesExisted := false
	// Define the GroupVersionResource for VolumeSnapshotClass
	gvr := schema.GroupVersionResource{
		Group:    "snapshot.storage.k8s.io",
		Version:  "v1",
		Resource: "volumesnapshotclasses",
	}
	resourceClient := dynamicClientSet.Resource(gvr)
	resourceList, err := resourceClient.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Error describing custom resource: %s\n", err)
		fmt.Printf("The crds volumesnapshots is not existed in shoot cluster")
		return volumeSnapshotClassesExisted, err
	}
	for _, item := range resourceList.Items {
		metadata, found, err := unstructured.NestedMap(item.Object, "metadata")
		if err != nil || !found {
			fmt.Println("Error accessing metadata:", err)
			continue
		}
		// get name of snapshot
		className, found, err := unstructured.NestedString(metadata, "name")
		if err != nil || !found {
			fmt.Println("Error accessing metadata name:", err)
			continue
		}
		if className == "csi-cinder-snapclass" {
			volumeSnapshotClassesExisted = true
			break
		}
	}
	return volumeSnapshotClassesExisted, nil
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
			pvSnapshot.SourcePvcName = pvcName
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
			pvSnapshot.SnapshotContentName = Unknown
		} else {
			pvSnapshot.SnapshotContentName = snapshotContent
		}
		// get creation time
		creationTimestamp, found, err := unstructured.NestedString(status, "creationTime")
		if err != nil || !found {
			//fmt.Println("Error accessing metadata creationTimestamp:", err)
			pvSnapshot.CreationTime = Unknown
			_, _ = getVolumeSnapShot(dynamicClienSet, resourceNamespace)
		} else {
			convertCreationTimeStamp, err := convertToUTCPlus7(creationTimestamp)
			if err != nil {
				convertCreationTimeStamp = creationTimestamp
			}
			pvSnapshot.CreationTime = convertCreationTimeStamp
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
			//fmt.Println("Error accessing status restoreSize:", err)
			pvSnapshot.RestoreSize = Unknown
		} else {
			pvSnapshot.RestoreSize = restoreSize
		}

		// add to return list
		pvSnapshotItemList = append(pvSnapshotItemList, pvSnapshot)
	}
	return pvSnapshotItemList, nil
}

func convertToUTCPlus7(timeStr string) (string, error) {
	returnTime := ""
	// Parse the input time string as a time.Time object
	utcTime, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return "", err
	}

	// Define the UTC+7 time zone
	utcPlus7 := time.FixedZone("UTC+7", 7*60*60)

	// Convert the UTC time to UTC+7
	localTime := utcTime.In(utcPlus7)
	returnTime = localTime.Format(time.RFC3339)
	// Return the converted time as a string in ISO 8601 format
	return returnTime, nil
}

func calculateDuration(timeStr string) (time.Duration, error) {
	// Parse the input time string as a time.Time object
	parsedTime, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		return 0, err
	}

	// Get the current time
	now := time.Now()

	// Calculate the duration between the parsed time and the current time
	duration := now.Sub(parsedTime)

	return duration, nil
}

// // get pvSnapshot crds
// func (r *CreateSnapshotReconciler) getPvSnapshotCRDS(dynamicClient *dynamic.DynamicClient, namespace string) (string, error) {
// 	pvSnapshotCrdName := ""

// 	return pvSnapshotCrdName, nil
// }

// ValidateHibernationCronSpec validates a cron specification of a hibernation schedule.
func ValidateSchedulerCronSpec(seenSpecs sets.String, spec string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	_, err := cron.ParseStandard(spec)
	switch {
	case err != nil:
		allErrs = append(allErrs, field.Invalid(fldPath, spec, fmt.Sprintf("not a valid cron spec: %v", err)))
	case seenSpecs.Has(spec):
		allErrs = append(allErrs, field.Duplicate(fldPath, spec))
	default:
		seenSpecs.Insert(spec)
	}

	return allErrs
}

// ValidateHibernationCronSpec validates a cron specification of a hibernation schedule.
func ValidateCronSpec(spec string) (cron.Schedule, error) {
	parseCron, err := cron.ParseStandard(spec)
	if err != nil {
		return parseCron, err
	}
	return parseCron, nil
}

// ValidateHibernationScheduleLocation validates that the location of a HibernationSchedule is correct.
func ValidateScheduleLocation(location string) (*time.Location, error) {
	parseLocation, err := time.LoadLocation(location)
	if err != nil {
		return parseLocation, err
	}
	return parseLocation, nil
}

// next returns the time in UTC from the schedule, that is immediately after the input time 't'.
// The input 't' is converted in the schedule's location before any calculations are done.
func next(schedule cron.Schedule, location *time.Location, t time.Time) time.Time {
	return schedule.Next(t.In(location)).UTC()
}

// previous returns the time in UTC from the schedule that is immediately before 'to' and after 'from'.
// Nil is returned if no such time can be found.
// The input times - 'to' and 'from' are converted in the schedule's location before any calculation is done.
func previous(schedule cron.Schedule, location *time.Location, t time.Time) time.Time {
	tInLocation := t.In(location)
	var previousTime time.Time

	// Iterate over the schedule to find the previous time
	for snapshotTime := schedule.Next(tInLocation.Add(-time.Hour * 24 * 365)); !snapshotTime.After(tInLocation); snapshotTime = schedule.Next(snapshotTime) {
		previousTime = snapshotTime
	}

	return previousTime.UTC()
}

func previousSnapshotDuration(schedule cron.Schedule, location *time.Location, now time.Time) time.Duration {

	previousTime := previous(schedule, location, now)

	return now.Sub(previousTime)
}

func nextSnapshotDuration(schedule cron.Schedule, location *time.Location, now time.Time) time.Duration {
	timeStamp := next(schedule, location, now)
	return timeStamp.Add(nextScheduleDelta).Sub(now)
}

func convertTimeNow2String(now time.Time) string {
	// Load the location for Asia/Bangkok
	location, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		fmt.Println("Error loading location:", err)
		return ""
	}

	// Convert the current time to the Asia/Bangkok timezone
	nowInBangkok := now.In(location)

	// Format the time to the desired format YY-MM-DD-hh-mm
	converted := nowInBangkok.Format("0601021504") // "06" for YY, "01" for MM, "02" for DD, "15" for hh, "04" for mm

	return converted
}

func getPvSnapshotListPerNamespace(dynamicClienSet *dynamic.DynamicClient, namespace string) ([]snapshotv1beta1.PvSnapshotItem, error) {
	snapshotList := []snapshotv1beta1.PvSnapshotItem{}

	klog.Infof("Checking snapshot in namespace: %s", namespace)
	resp, err := getVolumeSnapShot(dynamicClienSet, namespace)
	if err != nil {
		fmt.Printf("Unable to get Snapshot List in shoot cluster namespace: %s", namespace)
		return snapshotList, err
	}
	if len(resp) != 0 {
		snapshotList = append(snapshotList, resp...)
	} else {
		klog.Infof("There is no PV Snapshot in ns: %s", namespace)
	}

	return snapshotList, nil
}

func getSeedSnapshotList(ctx context.Context, c client.Client) (snapshotv1beta1.SnapshotList, error) {
	snapshotList := &snapshotv1beta1.SnapshotList{}
	if err := c.List(ctx, snapshotList); err != nil {
		klog.Errorf("Error listing Snapshots: %s", err)
		return *snapshotList, err
	}
	return *snapshotList, nil
}

func getSeedRestoredPvcList(ctx context.Context, c client.Client) (snapshotv1beta1.RestorePvcList, error) {
	restoreList := &snapshotv1beta1.RestorePvcList{}
	if err := c.List(ctx, restoreList); err != nil {
		klog.Errorf("Error listing Snapshots: %s", err)
		return *restoreList, nil
	}
	return *restoreList, nil
}

func getPvSnapshotStatus(dynamicClienSet *dynamic.DynamicClient, namespaceList []string) ([]snapshotv1beta1.SnapshotStatus, error) {
	pvSnapshotStatus := []snapshotv1beta1.SnapshotStatus{}
	for _, ns := range namespaceList {
		//klog.Infof("Checking snapshot in namespace: %s", ns)
		resp, err := getVolumeSnapShot(dynamicClienSet, ns)
		if err != nil {
			fmt.Printf("Unable to get Snapshot List in shoot cluster namespace: %s", ns)
			continue
		}
		if len(resp) != 0 {
			for _, item := range resp {
				addSnapshot := snapshotv1beta1.SnapshotStatus{
					SourcePvcName:           item.SourcePvcName,
					SnapshotName:            item.SnapshotName,
					Namespace:               item.Namespace,
					VolumeSnapshotClassName: item.VolumeSnapshotClassName,
					SnapshotContentName:     item.SnapshotContentName,
					CreationTime:            item.CreationTime,
					ReadyToUse:              item.ReadyToUse,
					RestoreSize:             item.RestoreSize,
				}
				pvSnapshotStatus = append(pvSnapshotStatus, addSnapshot)
			}
		} else {
			klog.Infof("There is no PV Snapshot in ns: %s", ns)
		}
	}
	return pvSnapshotStatus, nil
}

// envFromConfigMap creates an EnvVar from a ConfigMap key.
// It reduces boilerplate code.
func envFromConfigMap(env string, configMapName string, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: env,
		ValueFrom: &corev1.EnvVarSource{
			ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				Key:                  key,
			},
		},
	}
}

// checkRestorePvcInShoot check if there is pvc in shoot
// mapping with crds restorePvcs in seed
func checkRestorePvcInShoot(ctx context.Context, shootClientSet *kubernetes.Clientset, restorePvcSeed snapshotv1beta1.RestorePvc) (bool, error) {
	restorePvcShootNs := restorePvcSeed.Status.DesNamespace

	restorePvcShoot, err := shootClientSet.CoreV1().PersistentVolumeClaims(restorePvcShootNs).Get(ctx, restorePvcSeed.Name, metav1.GetOptions{})
	if err != nil {
		return true, nil
	}

	if restorePvcShoot == nil {
		return false, nil
	}

	return true, nil
}
