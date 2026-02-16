package controller

import (
	"context"
	"strconv"
	"time"
	"fmt"
	"strings"

	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type IngressReconciler struct {
	client.Client
	TinyMon *tinymon.Client
	Cluster string
}

func SetupIngressReconciler(mgr ctrl.Manager, tm *tinymon.Client, cluster string) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(&IngressReconciler{Client: mgr.GetClient(), TinyMon: tm, Cluster: cluster})
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("ingress", req.NamespacedName)

	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ingress deleted, removing from TinyMon")
			addr := resourceAddress(r.Cluster, "ingress", req.Namespace, req.Name)
			_ = r.TinyMon.DeleteHost(addr)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(ingress.Annotations) {
		addr := resourceAddress(r.Cluster, "ingress", ingress.Namespace, ingress.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress(r.Cluster, "ingress", ingress.Namespace, ingress.Name)
	httpInterval := checkInterval(ingress.Annotations, 300)
	certInterval := checkInterval(ingress.Annotations, 3600)
	t := defaultTopic(r.Cluster, "ingresses", ingress.Namespace, ingress.Annotations)
	expectedStatus := expectedStatusCode(ingress.Annotations)

	hosts := ingressHosts(&ingress)
	host := tinymon.Host{
		Name:        displayName(ingress.Annotations, ingress.Name),
		Address:     addr,
		Description: fmt.Sprintf("Ingress %s/%s (%s)", ingress.Namespace, ingress.Name, strings.Join(hosts, ", ")),
		Topic:       t,
		Enabled:     1,
	}

	log.Info("syncing ingress to TinyMon", "address", addr)
	if err := r.TinyMon.UpsertHost(host); err != nil {
		log.Error(err, "failed to upsert host")
		return ctrl.Result{}, err
	}

	// Create pull checks (TinyMon executes these, no result push from operator)
	httpPath := ""
	if p, ok := ingress.Annotations[AnnotationHTTPPath]; ok && p != "" {
		httpPath = strings.TrimRight(p, "/")
		if !strings.HasPrefix(httpPath, "/") {
			httpPath = "/" + httpPath
		}
	}

	for _, h := range hosts {
		url := "https://" + h + httpPath
		cfg := map[string]interface{}{"url": url}
		if expectedStatus > 0 {
			cfg["expected_status"] = expectedStatus
		}
		check := tinymon.Check{
			HostAddress:     addr,
			Type:            "http",
			Config:          cfg,
			IntervalSeconds: httpInterval,
			Enabled:         1,
		}
		if err := r.TinyMon.UpsertCheck(check); err != nil {
			log.Error(err, "failed to upsert http check", "host", h)
		}

		for _, tls := range ingress.Spec.TLS {
			for _, tlsHost := range tls.Hosts {
				if tlsHost == h {
					certCheck := tinymon.Check{
						HostAddress:     addr,
						Type:            "certificate",
						Config:          map[string]interface{}{"host": h, "port": 443},
						IntervalSeconds: certInterval,
						Enabled:         1,
					}
					if err := r.TinyMon.UpsertCheck(certCheck); err != nil {
						log.Error(err, "failed to upsert certificate check", "host", h)
					}
				}
			}
		}
	}

	// Create icecast_listeners checks if annotation is set (pull mode)
	if mounts, ok := ingress.Annotations[AnnotationIcecastMounts]; ok && mounts != "" {
		for _, mount := range strings.Split(mounts, ",") {
			mount = strings.TrimSpace(mount)
			if mount == "" {
				continue
			}
			for _, h := range hosts {
				iceCheck := tinymon.Check{
					HostAddress:     addr,
					Type:            "icecast_listeners",
					Config:          map[string]interface{}{"host": h, "port": 443, "mount": mount},
					IntervalSeconds: httpInterval,
					Enabled:         1,
				}
				if err := r.TinyMon.UpsertCheck(iceCheck); err != nil {
					log.Error(err, "failed to upsert icecast check", "host", h, "mount", mount)
				}
			}
		}
	}

	return ctrl.Result{RequeueAfter: time.Duration(httpInterval) * time.Second}, nil
}

func expectedStatusCode(annotations map[string]string) int {
	if annotations == nil {
		return 0
	}
	if v, ok := annotations[AnnotationExpectedStatus]; ok {
		if i, err := strconv.Atoi(v); err == nil && i >= 100 && i < 600 {
			return i
		}
	}
	return 0
}

func ingressHosts(ingress *networkingv1.Ingress) []string {
	var hosts []string
	for _, rule := range ingress.Spec.Rules {
		if rule.Host != "" {
			hosts = append(hosts, rule.Host)
		}
	}
	return hosts
}
