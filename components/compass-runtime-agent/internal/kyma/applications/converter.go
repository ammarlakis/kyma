package applications

import (
	"github.com/kyma-project/kyma/components/application-operator/pkg/apis/applicationconnector/v1alpha1"
	"github.com/kyma-project/kyma/components/application-operator/pkg/normalization"
	"github.com/kyma-project/kyma/components/compass-runtime-agent/internal/k8sconsts"
	"github.com/kyma-project/kyma/components/compass-runtime-agent/internal/kyma/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const defaultDescription = "Description not provided"

const (
	connectedAppLabelKey = "connected-app"
	managedByLabelKey    = "applicationconnector.kyma-project.io/managed-by"
	managedByLabelValue  = "compass-runtime-agent"
)

//go:generate mockery --name=Converter
type Converter interface {
	Do(application model.Application) v1alpha1.Application
}

type converter struct {
	nameResolver             k8sconsts.NameResolver
	centralGatewayServiceUrl string
	appSkipTLSVerify         bool
}

func NewConverter(nameResolver k8sconsts.NameResolver, centralGatewayServiceUrl string, skipVerify bool) Converter {
	return converter{nameResolver: nameResolver,
		centralGatewayServiceUrl: centralGatewayServiceUrl,
		appSkipTLSVerify:         skipVerify,
	}
}

func (c converter) Do(application model.Application) v1alpha1.Application {

	prepareLabels := func(directorLabels model.Labels) map[string]string {
		labels := make(map[string]string)
		labels[connectedAppLabelKey] = application.Name
		return labels
	}

	description := application.Description
	if description == "" {
		description = defaultDescription
	}

	return v1alpha1.Application{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Application",
			APIVersion: "applicationconnector.kyma-project.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   application.Name,
			Labels: map[string]string{managedByLabelKey: managedByLabelValue},
		},
		Spec: v1alpha1.ApplicationSpec{
			Description:      description,
			SkipInstallation: false,
			SkipVerify:       c.appSkipTLSVerify, // Taken from config. Maybe later we use value from labels of the Director's app
			Labels:           prepareLabels(application.Labels),
			Services:         c.toServices(application.Name, application.APIPackages),
			CompassMetadata:  c.toCompassMetadata(application.ID, application.SystemAuthsIDs),
		},
	}
}

func (c converter) toServices(applicationName string, packages []model.APIPackage) []v1alpha1.Service {
	services := make([]v1alpha1.Service, 0, len(packages))

	for _, p := range packages {
		services = append(services, c.toService(applicationName, p))
	}

	return services
}

func (c converter) toService(applicationName string, apiPackage model.APIPackage) v1alpha1.Service {

	description := defaultDescription
	if apiPackage.Description != nil && *apiPackage.Description != "" {
		description = *apiPackage.Description
	}

	return v1alpha1.Service{
		ID:                        apiPackage.ID,
		Identifier:                "", // not available in the Director's API
		Name:                      normalization.NormalizeServiceNameWithId(apiPackage.Name, apiPackage.ID),
		AuthCreateParameterSchema: apiPackage.InstanceAuthRequestInputSchema,
		DisplayName:               apiPackage.Name,
		Description:               description,
		Entries:                   c.toServiceEntries(applicationName, apiPackage),
	}
}

func (c converter) toServiceEntries(applicationName string, apiPackage model.APIPackage) []v1alpha1.Entry {
	entries := make([]v1alpha1.Entry, 0, len(apiPackage.APIDefinitions)+len(apiPackage.EventDefinitions))

	for _, apiDefinition := range apiPackage.APIDefinitions {
		entries = append(entries, c.toAPIEntry(applicationName, apiPackage, apiDefinition))
	}

	for _, eventAPIDefinition := range apiPackage.EventDefinitions {
		entries = append(entries, c.toEventServiceEntry(eventAPIDefinition))
	}

	return entries
}

func (c converter) toAPIEntry(applicationName string, apiPackage model.APIPackage, apiDefinition model.APIDefinition) v1alpha1.Entry {

	getApiType := func() string {
		if apiDefinition.APISpec != nil {
			return string(apiDefinition.APISpec.Type)
		}

		return ""
	}

	entry := v1alpha1.Entry{
		ID:                          apiDefinition.ID,
		Name:                        apiDefinition.Name,
		Type:                        SpecAPIType,
		ApiType:                     getApiType(),
		TargetUrl:                   apiDefinition.TargetUrl,
		CentralGatewayUrl:           c.toCentralGatewayURL(applicationName, apiPackage.Name, apiDefinition.Name),
		SpecificationUrl:            "", // Director returns BLOB here
		Credentials:                 c.toCredential(applicationName, apiPackage),
		RequestParametersSecretName: c.toRequestParametersSecretName(applicationName, apiPackage),
	}

	return entry
}

func (c converter) toRequestParametersSecretName(applicationName string, apiPackage model.APIPackage) string {
	if apiPackage.DefaultInstanceAuth != nil && apiPackage.DefaultInstanceAuth.RequestParameters != nil && !apiPackage.DefaultInstanceAuth.RequestParameters.IsEmpty() {
		return c.nameResolver.GetRequestParametersSecretName(applicationName, apiPackage.ID)
	}
	return ""
}

func (c converter) toCentralGatewayURL(applicationName string, apiPackageName string, apiDefinitionName string) string {

	return c.centralGatewayServiceUrl + "/" + applicationName +
		"/" + normalization.NormalizeName(apiPackageName) +
		"/" + normalization.NormalizeName(apiDefinitionName)
}

func (c converter) toCredential(applicationName string, apiPackage model.APIPackage) v1alpha1.Credentials {
	result := v1alpha1.Credentials{}

	if apiPackage.DefaultInstanceAuth != nil && apiPackage.DefaultInstanceAuth.Credentials != nil {
		csrfInfo := func(csrfInfo *model.CSRFInfo) *v1alpha1.CSRFInfo {
			if csrfInfo != nil {
				return &v1alpha1.CSRFInfo{TokenEndpointURL: csrfInfo.TokenEndpointURL}
			}
			return nil
		}
		if apiPackage.DefaultInstanceAuth.Credentials.Oauth != nil {
			return v1alpha1.Credentials{
				Type:              CredentialsOAuthType,
				SecretName:        c.nameResolver.GetCredentialsSecretName(applicationName, apiPackage.ID),
				AuthenticationUrl: apiPackage.DefaultInstanceAuth.Credentials.Oauth.URL,
				CSRFInfo:          csrfInfo(apiPackage.DefaultInstanceAuth.Credentials.CSRFInfo),
			}
		} else if apiPackage.DefaultInstanceAuth.Credentials.Basic != nil {
			return v1alpha1.Credentials{
				Type:       CredentialsBasicType,
				SecretName: c.nameResolver.GetCredentialsSecretName(applicationName, apiPackage.ID),
				CSRFInfo:   csrfInfo(apiPackage.DefaultInstanceAuth.Credentials.CSRFInfo),
			}
		}
	}
	return result
}

func (c converter) toEventServiceEntry(eventsDefinition model.EventAPIDefinition) v1alpha1.Entry {
	return v1alpha1.Entry{
		ID:               eventsDefinition.ID,
		Name:             eventsDefinition.Name,
		Type:             SpecEventsType,
		SpecificationUrl: "", // Director returns BLOB here
	}
}

func (c converter) toCompassMetadata(applicationID string, systemAuthsIDs []string) *v1alpha1.CompassMetadata {
	return &v1alpha1.CompassMetadata{
		ApplicationID: applicationID,
		Authentication: v1alpha1.Authentication{
			ClientIds: systemAuthsIDs,
		},
	}
}
