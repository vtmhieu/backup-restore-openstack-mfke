package controller

import (
	"slices"

	corev1 "k8s.io/api/core/v1"
)

type separatedPods struct {
	Running   []corev1.Pod // running, pending, unknown
	Succeeded []corev1.Pod // succeeded
	Failed    []corev1.Pod // failed
}

// separatePods separates the running pods from the stopped pods.
// The pods are sorted as a side effect.
func separatePods(pods *corev1.PodList) separatedPods {
	slices.SortFunc(pods.Items, func(a, b corev1.Pod) int {
		return int(a.CreationTimestamp.Unix() - b.CreationTimestamp.Unix())
	})

	r := separatedPods{
		Running:   make([]corev1.Pod, 0, len(pods.Items)/3),
		Succeeded: make([]corev1.Pod, 0, len(pods.Items)/3),
		Failed:    make([]corev1.Pod, 0, len(pods.Items)/3),
	}

	for _, pod := range pods.Items {
		switch pod.Status.Phase {
		case corev1.PodRunning, corev1.PodPending, corev1.PodUnknown:
			r.Running = append(r.Running, pod)
		case corev1.PodSucceeded:
			r.Succeeded = append(r.Succeeded, pod)
		case corev1.PodFailed:
			r.Failed = append(r.Failed, pod)
		}
	}

	return r
}

// updatePodImages returns true if the images of the wanted pod differ
// from the existing pod. The function will also modify the existing
// pod to match the wanted pod's images.
//
// If the number of containers in the pods differ, the function will
// return false and not modify the existing pod.
func updatePodImages(wanted, existing *corev1.Pod) (needsUpdating bool) {
	if len(wanted.Spec.Containers) != len(existing.Spec.Containers) {
		return
	}

	for i, c := range wanted.Spec.Containers {
		if c.Image != existing.Spec.Containers[i].Image {
			existing.Spec.Containers[i].Image = c.Image
			needsUpdating = true
		}
	}

	return
}
