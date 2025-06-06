package main

import (
	"encoding/json"
	"fmt"

	mapset "github.com/deckarep/golang-set/v2"
	kubewarden "github.com/kubewarden/policy-sdk-go"
	kubewarden_protocol "github.com/kubewarden/policy-sdk-go/protocol"
	"github.com/tidwall/gjson"
)

const (
	httpBadRequestStatusCode = 400
)

func validate(payload []byte) ([]byte, error) {
	// Create a ValidationRequest instance from the incoming payload
	validationRequest := kubewarden_protocol.ValidationRequest{}
	err := json.Unmarshal(payload, &validationRequest)
	if err != nil {
		logger.ErrorWith("Failed to parse validation request").Err("error", err).Write()
		return kubewarden.RejectRequest(
			kubewarden.Message(err.Error()),
			kubewarden.Code(httpBadRequestStatusCode))
	}

	// Create a Settings instance from the ValidationRequest object
	settings, err := NewSettingsFromValidationReq(&validationRequest)
	if err != nil {
		logger.ErrorWith("Failed to parse policy settings").Err("error", err).Write()
		return kubewarden.RejectRequest(
			kubewarden.Message(err.Error()),
			kubewarden.Code(httpBadRequestStatusCode))
	}

	// Access the **raw** JSON that describes the object
	podJSON := validationRequest.Request.Object

	logger.DebugWith("Validating Pod labels").
		String("operation", validationRequest.Request.Operation).
		String("kind", validationRequest.Request.Kind.Kind).
		Write()

	// NOTE 1
	data := gjson.GetBytes(
		podJSON,
		"metadata.labels")

	var validationErr error
	labels := mapset.NewThreadUnsafeSet[string]()
	data.ForEach(func(key, value gjson.Result) bool {
		// NOTE 2
		label := key.String()
		labels.Add(label)
		logger.InfoWith("Checking label").
			String("label", label).
			String("value", value.String()).
			Write()

		// NOTE 3
		validationErr = validateLabel(label, value.String(), &settings)
		if validationErr != nil {
			logger.WarnWith("Label validation failed").
				String("label", label).
				String("value", value.String()).
				Err("error", validationErr).
				Write()
		}

		// keep iterating if there are no errors
		return validationErr == nil
	})

	// NOTE 4
	if validationErr != nil {
		return kubewarden.RejectRequest(
			kubewarden.Message(validationErr.Error()),
			kubewarden.NoCode)
	}

	// NOTE 5
	for requiredLabel := range settings.ConstrainedLabels {
		if !labels.Contains(requiredLabel) {
			logger.WarnWith("Required label is missing").
				String("requiredLabel", requiredLabel).
				Write()
			return kubewarden.RejectRequest(
				kubewarden.Message(fmt.Sprintf("Constrained label %s not found inside of Pod", requiredLabel)),
				kubewarden.NoCode)
		}
	}

	logger.Info("Pod labels validation passed")
	return kubewarden.AcceptRequest()
}

func validateLabel(label, value string, settings *Settings) error {
	if settings.DeniedLabels.Contains(label) {
		return fmt.Errorf("label %s is on the deny list", label)
	}

	regExp, found := settings.ConstrainedLabels[label]
	if found {
		// This is a constrained label
		if !regExp.Match([]byte(value)) {
			return fmt.Errorf("the value of %s doesn't pass user-defined constraint", label)
		}
	}

	return nil
}
