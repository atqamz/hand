package webhost

import (
	"encoding/json"
	"regexp"
)

type trigger struct {
	Filter string `json:"url-filter"`
}

type action struct {
	Type string `json:"type"`
}

type rule struct {
	Trigger trigger `json:"trigger"`
	Action  action  `json:"action"`
}

func OriginRules(origin string) string {
	rules := []rule{
		{trigger{"^https?://"}, action{"block"}},
		{trigger{"^wss?://"}, action{"block"}},
		{trigger{"^" + regexp.QuoteMeta(origin) + "/"}, action{"ignore-previous-rules"}},
	}
	b, _ := json.Marshal(rules)
	return string(b)
}
