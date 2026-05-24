package main

func (c *codexClient) rememberNotificationItem(raw map[string]any) {
	method := stringValue(raw["method"])
	if method != "item/started" && method != "item/completed" {
		return
	}
	item := mapValue(mapValue(raw["params"])["item"])
	itemID := stringValue(item["id"])
	if itemID == "" {
		return
	}
	c.mu.Lock()
	c.items[itemID] = item
	c.mu.Unlock()
}

func (c *codexClient) enrichServerRequestEvent(ev *AstralEvent) {
	value := mapValue(ev.Normalized)
	if stringValue(value["kind"]) != "file_change" {
		return
	}
	itemID := stringValue(value["item_id"])
	if itemID == "" {
		return
	}
	c.mu.Lock()
	item := c.items[itemID]
	c.mu.Unlock()
	if len(item) == 0 {
		return
	}
	if changes := item["changes"]; changes != nil {
		value["changes"] = changes
		value["file_paths"] = codexFileChangePaths(changes)
	}
	if status := item["status"]; status != nil {
		value["status"] = status
	}
	ev.Normalized = value
}
