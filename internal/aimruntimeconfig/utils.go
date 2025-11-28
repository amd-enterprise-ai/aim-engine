package aimruntimeconfig

//
//const (
//	DefaultRuntimeConfigName = "default"
//)
//
//type RuntimeConfigWrapper struct {
//	ClusterConfig   *aimv1alpha1.AIMClusterRuntimeConfig
//	NamespaceConfig *aimv1alpha1.AIMRuntimeConfig
//}
//
//func GetRuntimeConfig(ctx context.Context, c client.Client, name string, namespace string) (RuntimeConfigWrapper, error) {
//	wrapper := RuntimeConfigWrapper{}
//
//	if namespace != "" {
//		namespaceConfig := aimv1alpha1.AIMRuntimeConfig{}
//		if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &namespaceConfig); err != nil && !apierrors.IsNotFound(err) {
//			return wrapper, err
//		} else if err == nil {
//			wrapper.NamespaceConfig = &namespaceConfig
//		}
//	}
//
//	clusterConfig := aimv1alpha1.AIMClusterRuntimeConfig{}
//	if err := c.Get(ctx, client.ObjectKey{Name: name}, &clusterConfig); err != nil && !apierrors.IsNotFound(err) {
//		return wrapper, err
//	} else if err == nil {
//		wrapper.ClusterConfig = &clusterConfig
//	}
//
//	// If the name is not `default` and no config was found, raise an exception
//	if name != DefaultRuntimeConfigName && wrapper.ClusterConfig == nil && wrapper.NamespaceConfig == nil {
//		groupResource := schema.GroupResource{
//			Group:    aimv1alpha1.GroupVersion.Group,
//			Resource: "aimclusterruntimeconfigs",
//		}
//
//		return wrapper, apierrors.NewNotFound(groupResource, name)
//	}
//
//	return wrapper, nil
//}
//
//func GetMergedRuntimeConfig(ctx context.Context, c client.Client, name string, namespace string) (*aimv1alpha1.AIMRuntimeConfigCommon, error) {
//	runtimeConfigWrapper, err := GetRuntimeConfig(ctx, c, name, namespace)
//	if err != nil {
//		return nil, err
//	}
//	return runtimeConfigWrapper.GetMerged(), nil
//}
//
//// MergeRuntimeConfigs merges two AIMRuntimeConfigCommon structs, with the priority config
//// taking precedence over the base config. Each field is merged individually, with priority
//// values overriding base values when both are present. Note that arrays are replaced entirely, no item-level
//// merging or additions are performed.
////
//// If only one config is non-nil, it is returned directly.
//// If both are nil, nil is returned.
////
//// Parameters:
////   - priority: The config with higher priority (overrides base values)
////   - base: The config with lower priority (provides defaults)
//func MergeRuntimeConfigs(priority *aimv1alpha1.AIMRuntimeConfigCommon, base *aimv1alpha1.AIMRuntimeConfigCommon) *aimv1alpha1.AIMRuntimeConfigCommon {
//	// If only priority exists, return it
//	if priority != nil && base == nil {
//		return priority
//	}
//
//	// If only base exists, return it
//	if base != nil && priority == nil {
//		return base
//	}
//
//	// If neither exists, return nil
//	if base == nil {
//		return nil
//	}
//
//	// Both exist - merge them with priority taking precedence
//	merged := *base
//
//	// Merge priority config into base config, with priority values overriding
//	// mergo.WithOverride ensures priority values take precedence.
//	// We can ignore the error as we control the input and their types.
//	_ = mergo.Merge(&merged, *priority, mergo.WithOverride)
//
//	return &merged
//}
//
//// GetMerged returns a merged view of the runtime configuration where namespace config
//// takes priority over cluster config. Each field is merged individually, with namespace
//// values overriding cluster values when both are present.
////
//// If only one config exists (cluster or namespace), it is returned directly.
//// If neither exists, nil is returned.
//func (w RuntimeConfigWrapper) GetMerged() *aimv1alpha1.AIMRuntimeConfigCommon {
//	var clusterConfig *aimv1alpha1.AIMRuntimeConfigCommon
//	var namespaceConfig *aimv1alpha1.AIMRuntimeConfigCommon
//
//	if w.ClusterConfig != nil {
//		clusterConfig = &w.ClusterConfig.Spec.AIMRuntimeConfigCommon
//	}
//
//	if w.NamespaceConfig != nil {
//		namespaceConfig = &w.NamespaceConfig.Spec.AIMRuntimeConfigCommon
//	}
//
//	// Merge with namespace taking priority over cluster
//	return MergeRuntimeConfigs(namespaceConfig, clusterConfig)
//}
//
//// MergeServiceRuntimeConfig merges runtime configuration from multiple sources with proper precedence.
//// The merge order is: ClusterRuntimeConfig → RuntimeConfig → Service fields (storage/routing)
//// Later values override earlier ones for conflicting fields.
////
//// This function is specifically designed for AIMService reconciliation, where services can override
//// certain runtime config fields (storage class, routing, PVC headroom) but not others (model discovery).
////
//// This function also handles migration of deprecated fields to their new grouped locations.
////
//// Parameters:
////   - serviceConfig: Service-level config overrides (from inlined AIMServiceRuntimeConfig fields, highest precedence)
////   - namespaceConfig: Namespace-scoped RuntimeConfig (middle precedence)
////   - clusterConfig: Cluster-scoped ClusterRuntimeConfig (lowest precedence)
////
//// Returns the merged configuration with all service-applicable fields resolved.
//func MergeServiceRuntimeConfig(
//	serviceConfig *aimv1alpha1.AIMServiceRuntimeConfig,
//	namespaceConfig *aimv1alpha1.AIMRuntimeConfigCommon,
//	clusterConfig *aimv1alpha1.AIMRuntimeConfigCommon,
//) aimv1alpha1.AIMRuntimeConfigCommon {
//	merged := aimv1alpha1.AIMRuntimeConfigCommon{}
//
//	// Start with cluster defaults
//	if clusterConfig != nil {
//		// Migrate deprecated storage fields before merging
//		migrateDeprecatedStorageFields(clusterConfig)
//		_ = mergo.Merge(&merged, clusterConfig)
//	}
//
//	// Override with namespace config
//	if namespaceConfig != nil {
//		// Migrate deprecated storage fields before merging
//		migrateDeprecatedStorageFields(namespaceConfig)
//		_ = mergo.Merge(&merged, namespaceConfig, mergo.WithOverride)
//	}
//
//	// Override with service-level config
//	// Note: We merge into the embedded AIMServiceRuntimeConfig field to only affect
//	// service-applicable fields (not Model.AutoDiscovery which doesn't apply to services)
//	if serviceConfig != nil {
//		_ = mergo.Merge(&merged.AIMServiceRuntimeConfig, serviceConfig, mergo.WithOverride)
//	}
//
//	return merged
//}
//
//func GetRuntimeConfigObservation(ctx context.Context, c client.Client, name string, namespace string) RuntimeConfigObservation {
//	merged, err := GetMergedRuntimeConfig(ctx, c, name, namespace)
//	return RuntimeConfigObservation{
//		MergedRuntimeConfig: merged,
//		Error:               err,
//	}
//}
