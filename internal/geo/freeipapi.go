package geo

// freeipapiResponse is the part of a https://free.freeipapi.com/api/v1/json
// response that we use. Its error format is undocumented, so failures are
// detected from the HTTP status and from missing location data.
type freeipapiResponse struct {
	IPAddress   string     `json:"ipAddress"`
	CountryName string     `json:"countryName"`
	CountryCode string     `json:"countryCode"`
	RegionName  string     `json:"regionName"`
	CityName    string     `json:"cityName"`
	ZipCode     flexString `json:"zipCode"`
}

func (r *freeipapiResponse) toLocation() (Location, *ProviderError) {
	return Location{
		ZipCode:     string(r.ZipCode),
		City:        r.CityName,
		Region:      r.RegionName,
		Country:     r.CountryName,
		CountryCode: r.CountryCode,
		IP:          r.IPAddress,
	}, nil
}
