package controller

import (
	"context"
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
}

func SetupIngressReconciler(mgr ctrl.Manager, tm *tinymon.Client) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Complete(&IngressReconciler{Client: mgr.GetClient(), TinyMon: tm})
}

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("ingress", req.NamespacedName)

	var ingress networkingv1.Ingress
	if err := r.Get(ctx, req.NamespacedName, &ingress); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ingress deleted, removing from TinyMon")
			addr := resourceAddress("ingress", req.Namespace, req.Name)
			_ = r.TinyMon.DeleteHost(addr)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !isEnabled(ingress.Annotations) {
		addr := resourceAddress("ingress", ingress.Namespace, ingress.Name)
		_ = r.TinyMon.DeleteHost(addr)
		return ctrl.Result{}, nil
	}

	addr := resourceAddress("ingress", ingress.Namespace, ingress.Name)
	httpInterval := checkInterval(ingress.Annotations, 300)
	certInterval := checkInterval(ingress.Annotations, 3600)
	defaultTopic := ingress.Namespace + "/ingresses"
	t := topic(ingress.Annotations)
	if t == "" {
		t = defaultTopic
	}

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
	for _, h := range hosts {
		url := "https://" + h
		check := tinymon.Check{
			HostAddress:     addr,
			Type:            "http",
			Config:          map[string]interface{}{"url": url},
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

	return ctrl.Result{}, nil
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
