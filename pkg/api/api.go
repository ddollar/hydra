package api

import (
	"context"
	"fmt"

	"github.com/ddollar/hydra/pkg/wan"
	"github.com/ddollar/stdapi"
)

type QualityHandler func() (int, error)
type QualityHandlers map[string]QualityHandler

type API struct {
	qualityHandlers QualityHandlers
	wan             *wan.Wan
}

func New(w *wan.Wan, qhs QualityHandlers) *API {
	return &API{
		qualityHandlers: qhs,
		wan:             w,
	}
}

func (a *API) Listen(ctx context.Context, proto, addr string) error {
	sa := stdapi.New("hydra", "hydra")

	sa.Route("GET", "/api/v1/status", func(c *stdapi.Context) error {
		var status struct {
			Active  string         `json:"active"`
			Quality map[string]int `json:"quality"`
		}

		status.Active = a.wan.Active()
		status.Quality = map[string]int{}

		for device, handler := range a.qualityHandlers {
			q, err := handler()
			if err != nil {
				return err
			}

			status.Quality[device] = q
		}

		return c.RenderJSON(status)
	})

	for device, handler := range a.qualityHandlers {
		sa.Route("GET", fmt.Sprintf("/api/v1/quality/%s", device), func(c *stdapi.Context) error {
			q, err := handler()
			if err != nil {
				return err
			}

			return c.RenderJSON(map[string]any{
				"device":  device,
				"quality": q,
			})
		})
	}

	return sa.Listen(proto, addr)
}
