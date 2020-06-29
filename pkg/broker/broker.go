package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Sectorbob/mlab-ns2/gae/ns/digest"
	"github.com/goccy/go-yaml"
	"github.com/gorilla/mux"
	"github.com/mongodb/go-client-mongodb-atlas/mongodbatlas"
	"github.com/mongodb/mongodb-atlas-service-broker/pkg/broker/credentials"
	"github.com/mongodb/mongodb-atlas-service-broker/pkg/broker/dynamicplans"
	"github.com/pivotal-cf/brokerapi/domain"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// Ensure broker adheres to the ServiceBroker interface.
var _ domain.ServiceBroker = new(Broker)

// Broker is responsible for translating OSB calls to Atlas API calls.
// Implements the domain.ServiceBroker interface making it easy to spin up
// an API server.
type Broker struct {
	logger      *zap.SugaredLogger
	whitelist   Whitelist
	credentials *credentials.Credentials
	baseURL     string
	mode        Mode
	catalog     *catalog
	client      *mongo.Client
}

// New creates a new Broker with a logger.
func New(logger *zap.SugaredLogger, credentials *credentials.Credentials, baseURL string, whitelist Whitelist, client *mongo.Client, mode Mode) *Broker {
	b := &Broker{
		logger:      logger,
		credentials: credentials,
		baseURL:     baseURL,
		whitelist:   whitelist,
		client:      client,
		mode:        mode,
	}

	if err := b.buildCatalog(); err != nil {
		logger.Fatalw("Cannot build service catalog", "error", err)
	}

	return b
}

func (b *Broker) parsePlan(planID string, rawParams json.RawMessage) (dp dynamicplans.Plan, err error) {
	sp, ok := b.catalog.plans[planID]
	if !ok {
		err = fmt.Errorf("plan ID %q not found in catalog", planID)
		return
	}

	tpl, ok := sp.Metadata.AdditionalMetadata["template"].(dynamicplans.TemplateContainer)
	if !ok {
		err = errors.New("plan ID %q does not contain a valid plan template")
		return
	}

	params := dynamicplans.DefaultCtx(b.credentials)

	// If params were passed we unmarshal them into the params object.
	if len(rawParams) > 0 {
		err = json.Unmarshal(rawParams, &params)
		if err != nil {
			return
		}
	}

	raw := new(bytes.Buffer)
	err = tpl.Execute(raw, params)
	if err != nil {
		return
	}

	b.logger.Infow("Parsed plan", "plan", raw.String(), "creds", b.credentials.Projects)

	if err = yaml.NewDecoder(raw).Decode(&dp); err != nil {
		return
	}

	return dp, nil
}

func (b *Broker) getInstanceState(ctx context.Context, instanceID string) (primitive.M, error) {
	i, err := b.GetInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	p, ok := i.Parameters.(primitive.D)
	if !ok {
		return nil, fmt.Errorf("instance metadata has the wrong type %T", i.Parameters)
	}

	return p.Map(), nil
}

func (b *Broker) getGroupIDByInstanceID(ctx context.Context, instanceID string) (string, error) {
	s, err := b.getInstanceState(ctx, instanceID)
	if err != nil {
		// no metadata - not an error in our case
		if err == mongo.ErrNoDocuments {
			return "", nil
		}
		return "", err
	}

	gidi, ok := s["groupID"]
	if !ok {
		return "", fmt.Errorf("groupID not found in instance metadata for %q", instanceID)
	}

	gid, ok := gidi.(string)
	if !ok {
		return "", fmt.Errorf("groupID from instance metadata has the wrong type %T", gidi)
	}

	return gid, nil
}

func (b *Broker) getClusterNameByInstanceID(ctx context.Context, instanceID string) (string, error) {
	if b.client == nil {
		return NormalizeClusterName(instanceID), nil
	}

	s, err := b.getInstanceState(ctx, instanceID)
	if err != nil {
		return "", err
	}

	ci, ok := s["clusterName"]
	if !ok {
		return "", fmt.Errorf("clusterName not found in instance metadata for %q", instanceID)
	}

	c, ok := ci.(string)
	if !ok {
		return "", fmt.Errorf("clusterName from instance metadata has the wrong type %T", ci)
	}

	return c, nil
}

func (b *Broker) getClient(ctx context.Context, instanceID string, planID string, rawParams json.RawMessage) (client *mongodbatlas.Client, gid string, err error) {
	switch b.mode {
	case BasicAuth:
		client, err = atlasClientFromContext(ctx)
		if err != nil {
			return
		}
		gid, err = groupIDFromContext(ctx)
		return client, gid, err

	case MultiGroup:
		// try to get groupID for existing instances
		gid, err = b.getGroupIDByInstanceID(ctx, instanceID)
		if err != nil {
			return
		}

		if gid != "" {
			break
		}

		// new instance: get groupID from params
		params := dynamicplans.DefaultCtx(b.credentials)

		// If params were passed we unmarshal them into the params object.
		if len(rawParams) > 0 {
			err = json.Unmarshal(rawParams, &params)
			if err != nil {
				return
			}
		}

		if params.Project.ID == "" {
			err = errors.New("project ID not found in rawParameters")
			return
		}

		gid = params.Project.ID

	case MultiGroupAutoPlans:
		gid, err = b.catalog.findGroupIDByPlanID(planID)
		if err != nil {
			return nil, gid, err
		}

	case DynamicPlans:
		// try to get groupID for existing instances
		gid, err = b.getGroupIDByInstanceID(ctx, instanceID)
		if err != nil {
			return
		}

		if gid != "" {
			break
		}

		// new instance: get groupID from params
		dp := dynamicplans.Plan{}
		dp, err = b.parsePlan(planID, rawParams)
		if err != nil {
			return
		}

		if dp.Project == nil {
			err = fmt.Errorf("missing Project in plan definition")
			return
		}

		// use existing project
		if dp.Project.ID != "" {
			gid = dp.Project.ID
			break
		}

	default:
		panic("invalid broker mode")
	}

	c, ok := b.credentials.Projects[gid]
	if !ok {
		return nil, gid, fmt.Errorf("credentials for project ID %q not found", gid)
	}

	hc, err := digest.NewTransport(c.PublicKey, c.PrivateKey).Client()
	if err != nil {
		return nil, gid, err
	}

	client, err = mongodbatlas.New(hc, mongodbatlas.SetBaseURL(b.baseURL))
	return client, gid, err
}

func (b *Broker) AuthMiddleware() mux.MiddlewareFunc {
	if b.credentials != nil {
		return authMiddleware(*b.credentials.Broker)
	}

	return simpleAuthMiddleware(b.baseURL)
}

func (b *Broker) GetDashboardURL(groupID, clusterName string) string {
	return fmt.Sprintf("%s/v2/%s#clusters/detail/%s", b.baseURL, groupID, clusterName)
}

// TODO: do something about this!
// atlasToAPIError converts an Atlas error to a OSB response error.
func atlasToAPIError(err error) error {
	// switch err {
	// case atlas.ErrClusterNotFound:
	// 	return apiresponses.ErrInstanceDoesNotExist
	// case atlas.ErrClusterAlreadyExists:
	// 	return apiresponses.ErrInstanceAlreadyExists
	// case atlas.ErrUserAlreadyExists:
	// 	return apiresponses.ErrBindingAlreadyExists
	// case atlas.ErrUserNotFound:
	// 	return apiresponses.ErrBindingDoesNotExist
	// case atlas.ErrUnauthorized:
	// 	return apiresponses.NewFailureResponse(err, http.StatusUnauthorized, "")
	// }

	// Fall back on returning the error again if no others match.
	// Will result in a 500 Internal Server Error.
	return err
}
