package controller

import "strconv"

const (
	AnnotationEnabled       = "tinymon.io/enabled"
	AnnotationName          = "tinymon.io/name"
	AnnotationTopic         = "tinymon.io/topic"
	AnnotationCheckInterval = "tinymon.io/check-interval"
)

func resourceAddress(kind, namespace, name string) string {
	if namespace == "" {
		return "k8s://" + kind + "/" + name
	}
	return "k8s://" + kind + "/" + namespace + "/" + name
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

func topic(annotations map[string]string) string {
	if annotations == nil {
		return ""
	}
	return annotations[AnnotationTopic]
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
