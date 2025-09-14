package k8s

import (
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	rbacv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/rbac/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"gopkg.in/yaml.v2"

	"github.com/modelcontextprotocol/registry/deploy/infra/pkg/providers"
)

func DeployMonitoringStack(ctx *pulumi.Context, cluster *providers.ProviderInfo, environment string, ingressNginx *helm.Chart) error {
	// Create namespace
	ns, err := corev1.NewNamespace(ctx, "monitoring", &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String("monitoring"),
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Deploy VictoriaMetrics
	_, err = helm.NewChart(ctx, "victoria-metrics", helm.ChartArgs{
		Chart:     pulumi.String("victoria-metrics-single"),
		Version:   pulumi.String("0.24.4"),
		Namespace: ns.Metadata.Name().Elem(),
		FetchArgs: helm.FetchArgs{
			Repo: pulumi.String("https://victoriametrics.github.io/helm-charts/"),
		},
		Values: pulumi.Map{
			"server": pulumi.Map{
				"retentionPeriod": pulumi.String("14d"),
				"resources": pulumi.Map{
					"requests": pulumi.Map{
						"memory": pulumi.String("128Mi"),
						"cpu":    pulumi.String("50m"),
					},
					"limits": pulumi.Map{
						"memory": pulumi.String("256Mi"),
					},
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Deploy VMAgent
	_, err = helm.NewChart(ctx, "victoria-metrics-agent", helm.ChartArgs{
		Chart:     pulumi.String("victoria-metrics-agent"),
		Version:   pulumi.String("0.25.3"),
		Namespace: ns.Metadata.Name().Elem(),
		FetchArgs: helm.FetchArgs{
			Repo: pulumi.String("https://victoriametrics.github.io/helm-charts/"),
		},
		Values: pulumi.Map{
			"remoteWrite": pulumi.Array{
				pulumi.Map{
					"url": pulumi.String("http://victoria-metrics-victoria-metrics-single-server:8428/api/v1/write"),
				},
			},
			"config": pulumi.Map{
				"global": pulumi.Map{
					"scrape_interval": pulumi.String("60s"),
				},
				"scrape_configs": pulumi.Array{
					pulumi.Map{
						"job_name": pulumi.String("mcp-registry"),
						"kubernetes_sd_configs": pulumi.Array{
							pulumi.Map{
								"role": pulumi.String("pod"),
								"namespaces": pulumi.Map{
									"names": pulumi.Array{pulumi.String("default")},
								},
							},
						},
						"relabel_configs": pulumi.Array{
							pulumi.Map{
								"source_labels": pulumi.Array{pulumi.String("__meta_kubernetes_pod_label_app")},
								"regex":         pulumi.String("mcp-registry.*"),
								"action":        pulumi.String("keep"),
							},
						},
					},
				},
			},
			"resources": pulumi.Map{
				"requests": pulumi.Map{
					"memory": pulumi.String("64Mi"),
					"cpu":    pulumi.String("25m"),
				},
				"limits": pulumi.Map{
					"memory": pulumi.String("128Mi"),
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Deploy VictoriaLogs for log storage
	err = deployVictoriaLogs(ctx, cluster, ns, environment)
	if err != nil {
		return err
	}

	// Deploy OpenTelemetry Collector DaemonSet
	err = deployOtelCollectorDaemonSet(ctx, cluster, ns, environment)
	if err != nil {
		return err
	}

	// Deploy Grafana
	return deployGrafana(ctx, cluster, ns, environment, ingressNginx)
}

// deployVictoriaLogs deploys VictoriaLogs for log storage
func deployVictoriaLogs(ctx *pulumi.Context, cluster *providers.ProviderInfo, ns *corev1.Namespace, environment string) error {
	// Deploy VictoriaLogs using Helm chart
	_, err := helm.NewChart(ctx, "victoria-logs", helm.ChartArgs{
		Chart:     pulumi.String("victoria-logs-single"),
		Version:   pulumi.String("0.6.4"),
		Namespace: ns.Metadata.Name().Elem(),
		FetchArgs: helm.FetchArgs{
			Repo: pulumi.String("https://victoriametrics.github.io/helm-charts/"),
		},
		Values: pulumi.Map{
			"server": pulumi.Map{
				"retentionPeriod": pulumi.String("15d"),
				"resources": pulumi.Map{
					"requests": pulumi.Map{
						"memory": pulumi.String("256Mi"),
						"cpu":    pulumi.String("100m"),
					},
					"limits": pulumi.Map{
						"memory": pulumi.String("2Gi"),
						"cpu":    pulumi.String("1000m"),
					},
				},
				"persistence": pulumi.Map{
					"enabled": pulumi.Bool(true),
					"size":    pulumi.String("20Gi"),
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	return nil
}

// deployOtelCollectorDaemonSet deploys OpenTelemetry Collector as DaemonSet
func deployOtelCollectorDaemonSet(ctx *pulumi.Context, cluster *providers.ProviderInfo, ns *corev1.Namespace, environment string) error {
	// Create ServiceAccount for OTEL Collector
	serviceAccount, err := corev1.NewServiceAccount(ctx, "otel-collector", &corev1.ServiceAccountArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("otel-collector"),
			Namespace: ns.Metadata.Name(),
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Create ClusterRole for log access
	clusterRole, err := rbacv1.NewClusterRole(ctx, "otel-collector", &rbacv1.ClusterRoleArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String("otel-collector"),
		},
		Rules: rbacv1.PolicyRuleArray{
			&rbacv1.PolicyRuleArgs{
				ApiGroups: pulumi.StringArray{pulumi.String("")},
				Resources: pulumi.StringArray{
					pulumi.String("pods"),
					pulumi.String("pods/log"),
					pulumi.String("nodes"),
					pulumi.String("namespaces"),
				},
				Verbs: pulumi.StringArray{
					pulumi.String("get"),
					pulumi.String("list"),
					pulumi.String("watch"),
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Create ClusterRoleBinding
	_, err = rbacv1.NewClusterRoleBinding(ctx, "otel-collector", &rbacv1.ClusterRoleBindingArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String("otel-collector"),
		},
		RoleRef: &rbacv1.RoleRefArgs{
			ApiGroup: pulumi.String("rbac.authorization.k8s.io"),
			Kind:     pulumi.String("ClusterRole"),
			Name:     clusterRole.Metadata.Name().Elem(),
		},
		Subjects: rbacv1.SubjectArray{
			&rbacv1.SubjectArgs{
				Kind:      pulumi.String("ServiceAccount"),
				Name:      serviceAccount.Metadata.Name().Elem(),
				Namespace: ns.Metadata.Name(),
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Create OpenTelemetry Collector configuration
	otelConfig := map[string]interface{}{
		"receivers": map[string]interface{}{
			"filelog": map[string]interface{}{
				"include":           []string{"/var/log/pods/default*/*/*.log"},
				"exclude":           []string{"/var/log/pods/*/*-collector-*/*.log"},
				"start_at":          "end",
				"include_file_path": true,
				"include_file_name": false,
				"operators": []map[string]interface{}{
					{
						"type":       "regex_parser",
						"id":         "extract_metadata_from_filepath",
						"regex":      `^.*\/(?P<namespace>[^_]+)_(?P<pod_name>[^_]+)_(?P<uid>[a-f0-9\-]{36})\/(?P<container_name>[^\._]+)\/(?P<restart_count>\d+)\.log`,
						"parse_from": "attributes[\"log.file.path\"]",
						"cache": map[string]interface{}{
							"size": 128,
						},
					},
					{
						"type": "move",
						"from": "attributes.container_name",
						"to":   "resource[\"k8s.container.name\"]",
					},
					{
						"type": "move",
						"from": "attributes.namespace",
						"to":   "resource[\"k8s.namespace.name\"]",
					},
					{
						"type": "move",
						"from": "attributes.pod_name",
						"to":   "resource[\"k8s.pod.name\"]",
					},
					{
						"type": "move",
						"from": "attributes.restart_count",
						"to":   "resource[\"k8s.container.restart_count\"]",
					},
					{
						"type": "move",
						"from": "attributes.uid",
						"to":   "resource[\"k8s.pod.uid\"]",
					},
				},
			},
		},
		"processors": map[string]interface{}{
			"batch": map[string]interface{}{},
			"k8sattributes": map[string]interface{}{
				"auth_type":   "serviceAccount",
				"passthrough": false,
				"filter": map[string]interface{}{
					"node_from_env_var": "KUBERNETES_NODE_NAME",
				},
				"extract": map[string]interface{}{
					"metadata": []string{
						"k8s.pod.name",
						"k8s.pod.uid",
						"k8s.deployment.name",
						"k8s.namespace.name",
						"k8s.node.name",
						"k8s.pod.start_time",
						"k8s.cluster.uid",
					},
					"labels": []map[string]interface{}{
						{
							"tag_name": "app",
							"key":      "app",
							"from":     "pod",
						},
					},
				},
				"pod_association": []map[string]interface{}{
					{
						"sources": []map[string]interface{}{
							{
								"from": "resource_attribute",
								"name": "k8s.pod.name",
							},
							{
								"from": "resource_attribute",
								"name": "k8s.namespace.name",
							},
						},
					},
				},
			},
			"filter/logging_services": map[string]interface{}{
				"error_mode": "ignore",
				"logs": map[string]interface{}{
					"log_record": []string{
						`resource.attributes["k8s.namespace.name"] != "default"`,
					},
				},
			},
		},
		"exporters": map[string]interface{}{
			"otlphttp/victorialogs": map[string]interface{}{
				"logs_endpoint": "http://victoria-logs-victoria-logs-single-server:9428/insert/opentelemetry/v1/logs",
				"headers": map[string]interface{}{
					"VL-Msg-Field":     "body",
					"VL-Time-Field":    "timestamp",
					"VL-Stream-Fields": "k8s.namespace.name,k8s.pod.name,k8s.container.name,log.iostream",
				},
				"timeout": "10s",
				"retry_on_failure": map[string]interface{}{
					"enabled":          true,
					"initial_interval": "5s",
					"max_interval":     "30s",
					"max_elapsed_time": "300s",
				},
				"sending_queue": map[string]interface{}{
					"enabled":       true,
					"num_consumers": 10,
					"queue_size":    50,
				},
			},
		},
		"service": map[string]interface{}{
			"pipelines": map[string]interface{}{
				"logs": map[string]interface{}{
					"receivers":  []string{"filelog"},
					"processors": []string{"batch", "k8sattributes", "filter/logging_services"},
					"exporters":  []string{"otlphttp/victorialogs"},
				},
			},
		},
	}

	otelConfigYAML, _ := yaml.Marshal(otelConfig)
	otelConfigMap, err := corev1.NewConfigMap(ctx, "otel-collector-config", &corev1.ConfigMapArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("otel-collector-config"),
			Namespace: ns.Metadata.Name(),
		},
		Data: pulumi.StringMap{
			"otel-collector-config.yaml": pulumi.String(string(otelConfigYAML)),
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Deploy OpenTelemetry Collector DaemonSet
	_, err = appsv1.NewDaemonSet(ctx, "otel-collector", &appsv1.DaemonSetArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("otel-collector"),
			Namespace: ns.Metadata.Name(),
			Labels: pulumi.StringMap{
				"app":         pulumi.String("otel-collector"),
				"environment": pulumi.String(environment),
			},
		},
		Spec: &appsv1.DaemonSetSpecArgs{
			Selector: &metav1.LabelSelectorArgs{
				MatchLabels: pulumi.StringMap{
					"app": pulumi.String("otel-collector"),
				},
			},
			Template: &corev1.PodTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{
						"app": pulumi.String("otel-collector"),
					},
				},
				Spec: &corev1.PodSpecArgs{
					ServiceAccountName: serviceAccount.Metadata.Name(),
					Containers: corev1.ContainerArray{
						&corev1.ContainerArgs{
							Name:  pulumi.String("otel-collector"),
							Image: pulumi.String("otel/opentelemetry-collector-contrib:0.95.0"),
							Args: pulumi.StringArray{
								pulumi.String("--config=/etc/otelcol-contrib/otel-collector-config.yaml"),
							},
							Ports: corev1.ContainerPortArray{
								&corev1.ContainerPortArgs{
									Name:          pulumi.String("otlp"),
									ContainerPort: pulumi.Int(4317),
									Protocol:      pulumi.String("TCP"),
								},
								&corev1.ContainerPortArgs{
									Name:          pulumi.String("otlp-http"),
									ContainerPort: pulumi.Int(4318),
									Protocol:      pulumi.String("TCP"),
								},
							},
							Env: corev1.EnvVarArray{
								&corev1.EnvVarArgs{
									Name: pulumi.String("KUBERNETES_NODE_NAME"),
									ValueFrom: &corev1.EnvVarSourceArgs{
										FieldRef: &corev1.ObjectFieldSelectorArgs{
											FieldPath: pulumi.String("spec.nodeName"),
										},
									},
								},
							},
							Resources: &corev1.ResourceRequirementsArgs{
								Requests: pulumi.StringMap{
									"memory": pulumi.String("200Mi"),
									"cpu":    pulumi.String("100m"),
								},
								Limits: pulumi.StringMap{
									"memory": pulumi.String("400Mi"),
									"cpu":    pulumi.String("200m"),
								},
							},
							VolumeMounts: corev1.VolumeMountArray{
								&corev1.VolumeMountArgs{
									Name:      pulumi.String("config"),
									MountPath: pulumi.String("/etc/otelcol-contrib/otel-collector-config.yaml"),
									SubPath:   pulumi.String("otel-collector-config.yaml"),
									ReadOnly:  pulumi.Bool(true),
								},
								&corev1.VolumeMountArgs{
									Name:      pulumi.String("varlogpods"),
									MountPath: pulumi.String("/var/log/pods"),
									ReadOnly:  pulumi.Bool(true),
								},
								&corev1.VolumeMountArgs{
									Name:      pulumi.String("varlibdockercontainers"),
									MountPath: pulumi.String("/var/lib/docker/containers"),
									ReadOnly:  pulumi.Bool(true),
								},
							},
						},
					},
					Volumes: corev1.VolumeArray{
						&corev1.VolumeArgs{
							Name: pulumi.String("config"),
							ConfigMap: &corev1.ConfigMapVolumeSourceArgs{
								Name: otelConfigMap.Metadata.Name(),
							},
						},
						&corev1.VolumeArgs{
							Name: pulumi.String("varlogpods"),
							HostPath: &corev1.HostPathVolumeSourceArgs{
								Path: pulumi.String("/var/log/pods"),
							},
						},
						&corev1.VolumeArgs{
							Name: pulumi.String("varlibdockercontainers"),
							HostPath: &corev1.HostPathVolumeSourceArgs{
								Path: pulumi.String("/var/lib/docker/containers"),
							},
						},
					},
					Tolerations: corev1.TolerationArray{
						&corev1.TolerationArgs{
							Key:      pulumi.String("node-role.kubernetes.io/master"),
							Operator: pulumi.String("Exists"),
							Effect:   pulumi.String("NoSchedule"),
						},
						&corev1.TolerationArgs{
							Key:      pulumi.String("node-role.kubernetes.io/control-plane"),
							Operator: pulumi.String("Exists"),
							Effect:   pulumi.String("NoSchedule"),
						},
					},
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	return nil
}

func deployGrafana(ctx *pulumi.Context, cluster *providers.ProviderInfo, ns *corev1.Namespace, environment string, ingressNginx *helm.Chart) error {
	conf := config.New(ctx, "mcp-registry")
	grafanaSecret, err := corev1.NewSecret(ctx, "grafana-secrets", &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("grafana-secrets"),
			Namespace: ns.Metadata.Name(),
		},
		StringData: pulumi.StringMap{
			"GF_AUTH_GOOGLE_CLIENT_SECRET": conf.RequireSecret("googleOauthClientSecret"),
		},
		Type: pulumi.String("Opaque"),
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	grafanaPgCluster, err := apiextensions.NewCustomResource(ctx, "grafana-pg", &apiextensions.CustomResourceArgs{
		ApiVersion: pulumi.String("postgresql.cnpg.io/v1"),
		Kind:       pulumi.String("Cluster"),
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("grafana-pg"),
			Namespace: ns.Metadata.Name(),
			Labels: pulumi.StringMap{
				"app":         pulumi.String("grafana-pg"),
				"environment": pulumi.String(environment),
			},
		},
		OtherFields: map[string]any{
			"spec": map[string]any{
				"instances": 1,
				"storage": map[string]any{
					"size": "10Gi",
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Create VictoriaMetrics and VictoriaLogs datasources
	datasourcesConfig := map[string]interface{}{
		"apiVersion": 1,
		"datasources": []map[string]interface{}{
			{
				"name":      "VictoriaMetrics",
				"type":      "prometheus",
				"url":       "http://victoria-metrics-victoria-metrics-single-server:8428",
				"access":    "proxy",
				"isDefault": true,
			},
			{
				"name":   "VictoriaLogs",
				"type":   "loki",
				"url":    "http://victoria-logs-victoria-logs-single-server:9428",
				"access": "proxy",
				"jsonData": map[string]interface{}{
					"maxLines": 1000,
				},
			},
		},
	}

	datasourcesConfigYAML, _ := yaml.Marshal(datasourcesConfig)
	grafanaDataSourcesConfigMap, err := corev1.NewConfigMap(ctx, "grafana-datasources", &corev1.ConfigMapArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("grafana-datasources"),
			Namespace: ns.Metadata.Name(),
		},
		Data: pulumi.StringMap{
			"datasources.yaml": pulumi.String(string(datasourcesConfigYAML)),
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Deploy Grafana
	grafanaHost := "grafana." + environment + ".registry.modelcontextprotocol.io"
	_, err = helm.NewChart(ctx, "grafana", helm.ChartArgs{
		Chart:   pulumi.String("grafana"),
		Version: pulumi.String("9.4.4"),
		FetchArgs: &helm.FetchArgs{
			Repo: pulumi.String("https://grafana.github.io/helm-charts"),
		},
		Namespace: ns.Metadata.Name().Elem(),
		Values: pulumi.Map{
			"extraConfigmapMounts": pulumi.Array{
				pulumi.Map{
					"name":      pulumi.String("grafana-datasources"),
					"mountPath": pulumi.String("/etc/grafana/provisioning/datasources"),
					"configMap": grafanaDataSourcesConfigMap.Metadata.Name(),
					"readOnly":  pulumi.Bool(true),
				},
			},
			"grafana.ini": pulumi.Map{
				"server": pulumi.Map{
					"root_url": pulumi.String("https://" + grafanaHost),
				},
				"auth": pulumi.Map{
					"disable_login_form": pulumi.Bool(true),
				},
				"auth.basic": pulumi.Map{
					"enabled": pulumi.Bool(false),
				},
				"security": pulumi.Map{
					"disable_initial_admin_creation": pulumi.Bool(true),
				},
				"users": pulumi.Map{
					"auto_assign_org_role": pulumi.String("Admin"),
				},
				"auth.google": pulumi.Map{
					"enabled":            pulumi.Bool(true),
					"client_id":          pulumi.String("606636202366-tpjm7d5vpp4lp9helg5ld2vrcafnrgh7.apps.googleusercontent.com"),
					"hosted_domain":      pulumi.String("modelcontextprotocol.io"),
					"allowed_domains":    pulumi.String("modelcontextprotocol.io"),
					"skip_org_role_sync": pulumi.Bool(true),
				},
				"database": pulumi.Map{
					"type": pulumi.String("postgres"),
					"host": pulumi.String("grafana-pg-rw:5432"),
				},
			},
			"envValueFrom": pulumi.Map{
				"GF_AUTH_GOOGLE_CLIENT_SECRET": pulumi.Map{
					"secretKeyRef": pulumi.Map{
						"name": grafanaSecret.Metadata.Name(),
						"key":  pulumi.String("GF_AUTH_GOOGLE_CLIENT_SECRET"),
					},
				},
				"GF_DATABASE_USER": pulumi.Map{
					"secretKeyRef": pulumi.Map{
						"name": grafanaPgCluster.Metadata.Name().ApplyT(func(name *string) string {
							if name == nil {
								return "grafana-pg-app"
							}
							return *name + "-app"
						}).(pulumi.StringOutput),
						"key": pulumi.String("username"),
					},
				},
				"GF_DATABASE_PASSWORD": pulumi.Map{
					"secretKeyRef": pulumi.Map{
						"name": grafanaPgCluster.Metadata.Name().ApplyT(func(name *string) string {
							if name == nil {
								return "grafana-pg-app"
							}
							return *name + "-app"
						}).(pulumi.StringOutput),
						"key": pulumi.String("password"),
					},
				},
				"GF_DATABASE_NAME": pulumi.Map{
					"secretKeyRef": pulumi.Map{
						"name": grafanaPgCluster.Metadata.Name().ApplyT(func(name *string) string {
							if name == nil {
								return "grafana-pg-app"
							}
							return *name + "-app"
						}).(pulumi.StringOutput),
						"key": pulumi.String("dbname"),
					},
				},
			},
			"resources": pulumi.Map{
				"requests": pulumi.Map{
					"memory": pulumi.String("128Mi"),
					"cpu":    pulumi.String("50m"),
				},
				"limits": pulumi.Map{
					"memory": pulumi.String("256Mi"),
				},
			},
		},
	}, pulumi.Provider(cluster.Provider))
	if err != nil {
		return err
	}

	// Create ingress for external access
	_, err = networkingv1.NewIngress(ctx, "grafana-ingress", &networkingv1.IngressArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String("grafana-ingress"),
			Namespace: ns.Metadata.Name(),
			Annotations: pulumi.StringMap{
				"cert-manager.io/cluster-issuer": pulumi.String("letsencrypt-prod"),
				"kubernetes.io/ingress.class":    pulumi.String("nginx"),
			},
		},
		Spec: &networkingv1.IngressSpecArgs{
			Tls: networkingv1.IngressTLSArray{
				&networkingv1.IngressTLSArgs{
					Hosts:      pulumi.StringArray{pulumi.String(grafanaHost)},
					SecretName: pulumi.String("grafana-tls"),
				},
			},
			Rules: networkingv1.IngressRuleArray{
				&networkingv1.IngressRuleArgs{
					Host: pulumi.String(grafanaHost),
					Http: &networkingv1.HTTPIngressRuleValueArgs{
						Paths: networkingv1.HTTPIngressPathArray{
							&networkingv1.HTTPIngressPathArgs{
								Path:     pulumi.String("/"),
								PathType: pulumi.String("Prefix"),
								Backend: &networkingv1.IngressBackendArgs{
									Service: &networkingv1.IngressServiceBackendArgs{
										Name: pulumi.String("grafana"),
										Port: &networkingv1.ServiceBackendPortArgs{
											Number: pulumi.Int(80),
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, pulumi.Provider(cluster.Provider), pulumi.DependsOnInputs(ingressNginx.Ready))
	if err != nil {
		return err
	}

	ctx.Export("grafanaUrl", pulumi.Sprintf("https://%s", grafanaHost))
	return nil
}
