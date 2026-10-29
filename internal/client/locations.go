package client

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Location is a city monitors can be checked from.
type Location struct {
	ID          int64
	Name        string
	CountryID   int64
	CountryName string
	Continent   string
}

// locationNode is one node of GET /v1/location-target/tree: CONTINENT > COUNTRY > CITY.
type locationNode struct {
	Type     string         `json:"type"`
	Value    string         `json:"value"`
	Label    string         `json:"label"`
	Children []locationNode `json:"children"`
}

// ListLocations returns every city with a check location, from the clouds named in providers (the API's default,
// aws, gcp and azure, when empty), sorted by continent, country and city.
func (c *Client) ListLocations(ctx context.Context, providers []string) ([]Location, error) {
	q := url.Values{}
	if len(providers) > 0 {
		q.Set("providers", strings.Join(providers, ","))
	}
	var tree []locationNode
	if err := c.do(ctx, http.MethodGet, "/v1/location-target/tree", q, nil, &tree); err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	var out []Location
	for _, cont := range tree {
		for _, country := range cont.Children {
			countryID, _ := strconv.ParseInt(country.Value, 10, 64)
			for _, city := range country.Children {
				if !strings.EqualFold(city.Type, "CITY") {
					continue
				}
				id, err := strconv.ParseInt(city.Value, 10, 64)
				if err != nil || seen[id] {
					continue
				}
				seen[id] = true
				out = append(out, Location{
					ID:          id,
					Name:        city.Label,
					CountryID:   countryID,
					CountryName: country.Label,
					Continent:   cont.Label,
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Continent != b.Continent {
			return a.Continent < b.Continent
		}
		if a.CountryName != b.CountryName {
			return a.CountryName < b.CountryName
		}
		return a.Name < b.Name
	})
	return out, nil
}
