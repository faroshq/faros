package service

import (
	restfulspec "github.com/emicklei/go-restful-openapi/v2"
	"github.com/emicklei/go-restful/v3"
	"github.com/go-openapi/spec"
)

type SwaggerResource struct{}

func (s *SwaggerResource) RegisterTo(container *restful.Container) {
	config := restfulspec.Config{
		WebServices:                   container.RegisteredWebServices(), // you control what services are visible
		APIPath:                       "/apidocs.json",
		PostBuildSwaggerObjectHandler: enrichSwaggerObject,
	}
	container.Add(restfulspec.NewOpenAPIService(config))

}

func enrichSwaggerObject(swo *spec.Swagger) {
	swo.Info = &spec.Info{
		InfoProps: spec.InfoProps{
			Title:       "Faros",
			Description: "Resource for managing Faros workspaces",
			Contact: &spec.ContactInfo{
				ContactInfoProps: spec.ContactInfoProps{
					Name:  "Mangirdas",
					Email: "mangirdas@judeikis.LT",
					URL:   "https://faros.sh",
				},
			},
			License: &spec.License{
				LicenseProps: spec.LicenseProps{
					Name: "MIT",
					URL:  "http://mit.org",
				},
			},
			Version: "1.0.0",
		},
	}
	swo.Tags = []spec.Tag{{TagProps: spec.TagProps{
		Name:        "faros",
		Description: "Managing faros"}}}
}
