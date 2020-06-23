package broker

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/mongodb/go-client-mongodb-atlas/mongodbatlas"
	"github.com/mongodb/mongodb-atlas-service-broker/pkg/atlas"
	"github.com/mongodb/mongodb-atlas-service-broker/pkg/broker/dynamicplans"
	"github.com/pivotal-cf/brokerapi/domain"
)

// idPrefix will be prepended to service and plan IDs to ensure their uniqueness.
const idPrefix = "aosb-cluster"

// providerNames contains all the available cloud providers on which clusters
// may be provisioned. The available instance sizes for each provider are
// fetched dynamically from the Atlas API.
var (
	providerNames = []string{"AWS", "GCP", "AZURE", "TENANT"}

	// Hardcode the instance sizes for shared instances
	sharedService = domain.Service{
		ID:                   "aosb-cluster-service-tenant",
		Name:                 "mongodb-atlas-tenant",
		Description:          "Atlas cluster hosted on \"TENANT\"",
		Bindable:             true,
		InstancesRetrievable: false,
		BindingsRetrievable:  false,
		Metadata:             nil,
		PlanUpdatable:        true,
		Plans: []domain.ServicePlan{
			{
				ID:          "aosb-cluster-plan-tenant-m2",
				Name:        "M2",
				Description: "Instance size \"M2\"",
			},
			{
				ID:          "aosb-cluster-plan-tenant-m5",
				Name:        "M5",
				Description: "Instance size \"M5\"",
			},
		},
	}

	forbiddenSymbols = regexp.MustCompile("[^-a-zA-Z0-9]+")
)

// Services generates the service catalog which will be presented to consumers of the API.
func (b *Broker) Services(ctx context.Context) ([]domain.Service, error) {
	b.logger.Info("Retrieving service catalog")

	if b.catalog == nil {
		if err := b.buildCatalog(); err != nil {
			return nil, err
		}
	}

	return b.catalog.services, nil
}

func (b *Broker) buildCatalog() error {
	b.catalog = newCatalog()

	for _, providerName := range providerNames {
		whitelistedPlans, isWhitelisted := b.whitelist[providerName]
		if b.whitelist != nil && !isWhitelisted {
			continue
		}

		svc := domain.Service{}

		if providerName == "TENANT" {
			svc = sharedService
		} else {
			// TODO: temporary hack, replace with a proper private API client
			u, _ := url.Parse(b.baseURL)
			u.Path = ""
			provider, err := atlas.NewClient(u.String(), "", "", "").GetProvider(providerName)
			if err != nil {
				return err
			}

			svc = b.buildService(provider)
			b.catalog.providers[svc.ID] = *provider
		}

		if b.whitelist != nil {
			svc.Plans = b.catalog.applyWhitelist(svc.Plans, whitelistedPlans)
		}

		for _, p := range svc.Plans {
			b.catalog.plans[p.ID] = p
		}

		b.catalog.services = append(b.catalog.services, svc)
	}

	return nil
}

func (b *Broker) buildService(provider *atlas.Provider) (service domain.Service) {
	// Create a CLI-friendly and user-friendly name. Will be displayed in the
	// marketplace generated by the service catalog.
	catalogName := fmt.Sprintf("mongodb-atlas-%s", strings.ToLower(provider.Name))

	service = domain.Service{
		ID:                   serviceIDForProvider(provider.Name),
		Name:                 catalogName,
		Description:          fmt.Sprintf(`Atlas cluster hosted on "%s"`, provider.Name),
		Bindable:             true,
		InstancesRetrievable: false,
		BindingsRetrievable:  false,
		Metadata:             nil,
		PlanUpdatable:        true,
		Plans:                b.buildPlansForProvider(provider),
	}

	return service
}

// plansForProvider will convert the available instance sizes for a provider
// to service plans for the broker.
func (b *Broker) buildPlansForProvider(provider *atlas.Provider) []domain.ServicePlan {
	switch b.mode {
	case BasicAuth:
		return b.buildPlansForProviderStatic(provider)
	case MultiGroup:
		panic("not implemented")
	case MultiGroupAutoPlans:
		return b.buildPlansForProviderAuto(provider)
	case DynamicPlans:
		return b.buildPlansForProviderDynamic(provider)
	default:
		panic("unknown broker mode")
	}
}

func (*Broker) buildPlansForProviderStatic(provider *atlas.Provider) []domain.ServicePlan {
	var plans []domain.ServicePlan
	for _, instanceSize := range provider.InstanceSizes {
		plan := domain.ServicePlan{
			ID:          planIDForInstanceSize(provider.Name, instanceSize, ""),
			Name:        instanceSize.Name,
			Description: fmt.Sprintf("Instance size %q", instanceSize.Name),
			Metadata: &domain.ServicePlanMetadata{
				AdditionalMetadata: map[string]interface{}{
					"instanceSize": instanceSize,
				},
			},
		}
		plans = append(plans, plan)
		continue
	}

	return plans
}

func normalizeID(id string) string {
	return strings.ToLower(forbiddenSymbols.ReplaceAllString(id, "_"))
}

func (b *Broker) buildPlansForProviderAuto(provider *atlas.Provider) []domain.ServicePlan {
	var plans []domain.ServicePlan

	for _, instanceSize := range provider.InstanceSizes {
		for groupID, key := range b.credentials.Projects {
			id := groupID
			if key.Desc != "" {
				id = normalizeID(key.Desc)
			}

			plan := domain.ServicePlan{
				ID:          planIDForInstanceSize(provider.Name, instanceSize, groupID),
				Name:        fmt.Sprintf("%s-%s", instanceSize.Name, id),
				Description: fmt.Sprintf("Instance size %q", instanceSize.Name),
				Metadata: &domain.ServicePlanMetadata{
					AdditionalMetadata: map[string]interface{}{
						"groupID":      groupID,
						"instanceSize": instanceSize,
					},
				},
			}
			plans = append(plans, plan)
		}
	}

	return plans
}

func (b *Broker) buildPlansForProviderDynamic(provider *atlas.Provider) []domain.ServicePlan {
	var plans []domain.ServicePlan

	templates, err := dynamicplans.FromEnv()
	if err != nil {
		b.logger.Fatalw("could not read dynamic plans from environment", "error", err)
	}

	ctx := dynamicplans.DefaultCtx(b.credentials)
	ctx.Cluster.ProviderSettings = &mongodbatlas.ProviderSettings{
		ProviderName: provider.Name,
	}

	for _, template := range templates {
		raw := new(bytes.Buffer)
		err := template.Execute(raw, ctx)
		if err != nil {
			b.logger.Errorf("cannot execute template %q: %v", template.Name(), err)
			continue
		}

		b.logger.Info("Parsed plan: %s", raw.String())

		p := dynamicplans.Plan{}
		if err := yaml.NewDecoder(raw).Decode(&p); err != nil {
			b.logger.Errorw("cannot decode yaml template", "name", template.Name(), "error", err)
			continue
		}

		if p.Cluster == nil ||
			p.Cluster.ProviderSettings == nil ||
			p.Cluster.ProviderSettings.InstanceSizeName == "" {
			b.logger.Errorw(
				"invalid yaml template",
				"name", template.Name(),
				"error", ".cluster.providerSettings.instanceSizeName must not be empty",
			)
			continue
		}

		pn := p.Cluster.ProviderSettings.ProviderName
		if pn != "" && pn != provider.Name {
			continue
		}

		plan := domain.ServicePlan{
			ID:          planIDForDynamicPlan(provider.Name, p.Name),
			Name:        p.Name,
			Description: p.Description,
			Free:        p.Free,
			Metadata: &domain.ServicePlanMetadata{
				AdditionalMetadata: map[string]interface{}{
					"template":     template,
					"instanceSize": provider.InstanceSizes[p.Cluster.ProviderSettings.InstanceSizeName],
				},
			},
		}
		plans = append(plans, plan)
		continue
	}

	return plans
}

// serviceIDForProvider will generate a globally unique ID for a provider.
func serviceIDForProvider(providerName string) string {
	return fmt.Sprintf("%s-service-%s", idPrefix, strings.ToLower(providerName))
}

// planIDForInstanceSize will generate a globally unique ID for an instance size
// on a specific provider.
func planIDForInstanceSize(providerName string, instanceSize atlas.InstanceSize, groupID string) string {
	result := fmt.Sprintf("%s-plan-%s-%s", idPrefix, strings.ToLower(providerName), strings.ToLower(instanceSize.Name))

	if groupID == "" {
		return result
	}

	return fmt.Sprintf("%s-%s", result, groupID)
}

func planIDForDynamicPlan(providerName string, planName string) string {
	return fmt.Sprintf("%s-plan-%s-%s", idPrefix, strings.ToLower(providerName), strings.ToLower(planName))
}
