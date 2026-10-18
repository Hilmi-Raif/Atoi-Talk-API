package model

type PushDeviceRequest struct {
	Token        string `json:"token"`
	Platform     string `json:"platform"`
	DeviceSecret string `json:"device_secret"`
}
