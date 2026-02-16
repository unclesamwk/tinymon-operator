package controller

const (
	AnnotationEnabled = "tinymon.io/enabled"
	AnnotationName    = "tinymon.io/name"
	AnnotationTopic   = "tinymon.io/topic"
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
