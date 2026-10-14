package semantic

import "encoding/json"

// WithDocumentSource configures the portable delivery adapter at workspace
// construction, before the service is shared by HTTP handlers.
func (service *DeliveryService) WithDocumentSource(source SourceEntry) *DeliveryService {
	service.documentSource = &source
	return service
}
func (payload DeliveryPayload) MarshalJSON() ([]byte, error) {
	if payload.Document != nil {
		return json.Marshal(payload.Document)
	}
	type legacyPayload DeliveryPayload
	return json.Marshal(legacyPayload(payload))
}
