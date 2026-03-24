package controller

import (
	"strconv"
	"strings"
)

const (
	AnnotationEnabled       = "tinymon.io/enabled"
	AnnotationName          = "tinymon.io/name"
	AnnotationTopic         = "tinymon.io/topic"
	AnnotationCheckInterval = "tinymon.io/check-interval"
	AnnotationExpectedStatus = "tinymon.io/expected-status"
	AnnotationIcecastMounts  = "tinymon.io/icecast-mounts"
	AnnotationHTTPPath       = "tinymon.io/http-path"

	LabelPrefix = "tinymon.io/label-"
)

func resourceAddress(cluster, kind, namespace, name string) string {
	if namespace == "" {
		return "k8s://" + cluster + "/" + kind + "/" + name
	}
	return "k8s://" + cluster + "/" + kind + "/" + namespace + "/" + name
}

func defaultTopic(cluster, kind, namespace string, annotations map[string]string) string {
	if annotations != nil && annotations[AnnotationTopic] != "" {
		return annotations[AnnotationTopic]
	}
	if namespace == "" {
		return "Kubernetes/" + cluster + "/" + kind
	}
	return "Kubernetes/" + cluster + "/" + kind + "/" + namespace
}

func isEnabled(annotations map[string]string) bool {
	if annotations == nil {
		return false
	}
	return annotations[AnnotationEnabled] == "true"
}

func displayName(annotations map[string]string, fallback string) string {
	if annotations != nil && annotations[AnnotationName] != "" {
		return annotations[AnnotationName]
	}
	return fallback
}

func checkInterval(annotations map[string]string, defaultInterval int) int {
	if annotations == nil {
		return defaultInterval
	}
	if v, ok := annotations[AnnotationCheckInterval]; ok {
		if i, err := strconv.Atoi(v); err == nil && i >= 30 {
			return i
		}
	}
	return defaultInterval
}

// extractLabels extracts Kubernetes labels with prefix "tinymon.io/label-"
// and returns a clean map with the prefix stripped.
func extractLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	result := make(map[string]string)
	for k, v := range labels {
		if strings.HasPrefix(k, LabelPrefix) {
			key := strings.TrimPrefix(k, LabelPrefix)
			if key != "" {
				result[key] = v
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// buildLabels creates the labels map for a host by combining:
// - auto-generated labels (cluster, type)
// - user-defined labels from K8s labels with "tinymon.io/label-" prefix
// User-defined labels take precedence over auto-generated ones.
func buildLabels(cluster string, resourceType string, k8sLabels map[string]string) map[string]string {
	result := map[string]string{
		"cluster": cluster,
		"type":    resourceType,
	}
	// User-defined labels override auto-generated ones
	for k, v := range extractLabels(k8sLabels) {
		result[k] = v
	}
	return result
}
