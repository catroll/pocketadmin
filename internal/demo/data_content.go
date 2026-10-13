package demo

// workbench writes announcements, the personal workbench and the folder tree.
//
// It is the part of the dataset that makes the panel look lived-in rather than merely
// populated: an announcement somebody has read, a todo list with work in progress, and an
// inbox with something unread in it.

// workbench writes announcements, todos, messages and quick links.
func (s *Seeder) workbench() error {
	announcements := []struct {
		key, title, kind, level, audience string
		published                         bool
		daysAgo                           int
	}{
		{"ann.release", "Version 1.1 is out", "update", "normal", "all", true, 3},
		{"ann.maintenance", "Scheduled maintenance on Sunday", "maintenance", "important", "all", true, 1},
		{"ann.draft", "A draft nobody has published", "notice", "normal", "all", false, 0},
	}

	for _, announcement := range announcements {
		fields := map[string]any{
			"title":    announcement.title,
			"content":  "<p>" + announcement.title + " — example content from the demo dataset.</p>",
			"type":     announcement.kind,
			"level":    announcement.level,
			"audience": announcement.audience,
			"status":   "draft",
			"author":   s.id("user.carol"),
		}
		if announcement.published {
			fields["status"] = "published"
			// A published announcement carries the moment it went out, which is what the
			// screen orders by.
			fields["publishAt"] = s.daysAgo(announcement.daysAgo).UTC()
		}

		rec, err := s.create("announcements", fields)
		if err != nil {
			return err
		}
		s.remember(announcement.key, rec)

		// A published announcement has been read by some of the people who saw it, which
		// is what the read count on the screen is for.
		if !announcement.published {
			continue
		}
		for _, user := range []string{"user.alice", "user.bob", "user.dan"} {
			if _, err := s.create("announcement_reads", map[string]any{
				"announcement": rec.Id,
				"user":         s.id(user),
				"readAt":       s.daysAgo(announcement.daysAgo - 1).UTC(),
			}); err != nil {
				return err
			}
		}
	}

	todos := []struct {
		owner, title, status, priority string
		dueInDays                      int
	}{
		{"user.alice", "Reply to the damaged-headphones ticket", "in_progress", "high", 1},
		{"user.alice", "Check the low-stock list", "pending", "normal", 3},
		{"user.bob", "Book a stocktake for the travel range", "pending", "normal", 7},
		{"user.bob", "Return the damaged notebook to the supplier", "done", "low", -2},
		{"user.carol", "Write the spring campaign summary", "in_progress", "urgent", 0},
		{"user.dan", "Reconcile last month's settlements", "pending", "high", 2},
	}

	for _, todo := range todos {
		if _, err := s.create("todos", map[string]any{
			"title":       todo.title,
			"description": "<p>Example task from the demo dataset.</p>",
			"owner":       s.id(todo.owner),
			"status":      todo.status,
			"priority":    todo.priority,
			"dueAt":       s.daysAgo(-todo.dueInDays).UTC(),
		}); err != nil {
			return err
		}
	}

	// One message with several recipients, so the inbox is not a list of one.
	message, err := s.create("messages", map[string]any{
		"sender":   s.id("user.carol"),
		"title":    "Spring campaign is live",
		"content":  "<p>The campaign is live and the coupons are issuing. Example content.</p>",
		"type":     "notice",
		"level":    "info",
		"link":     "/promotions/coupons",
		"audience": "direct",
	})
	if err != nil {
		return err
	}

	recipients := []struct {
		user, status string
	}{
		{"user.alice", "read"},
		{"user.bob", "unread"},
		{"user.dan", "unread"},
	}
	for _, recipient := range recipients {
		fields := map[string]any{
			"message": message.Id,
			"user":    s.id(recipient.user),
		}
		if recipient.status == "read" {
			fields["readAt"] = s.daysAgo(1).UTC()
		}
		if _, err := s.create("message_recipients", fields); err != nil {
			return err
		}
	}

	links := []struct {
		owner, title, path, icon string
	}{
		{"user.alice", "Low stock", "/catalog/products", "fas fa-triangle-exclamation"},
		{"user.alice", "Orders awaiting payment", "/orders?status=pending_payment", "fas fa-hourglass-half"},
		{"user.dan", "Unreconciled payments", "/payments?unreconciled=1", "fas fa-scale-balanced"},
	}
	for i, link := range links {
		if _, err := s.create("quick_links", map[string]any{
			"user":      s.id(link.owner),
			"title":     link.title,
			"path":      link.path,
			"icon":      link.icon,
			"sortOrder": (i + 1) * 10,
		}); err != nil {
			return err
		}
	}

	visits := []struct {
		owner, path, title string
		hoursAgo           int
	}{
		{"user.alice", "/orders", "Orders", 2},
		{"user.alice", "/catalog/products", "Products", 3},
		{"user.bob", "/catalog/products", "Products", 5},
		{"user.carol", "/promotions/coupons", "Coupons", 1},
	}
	for _, visit := range visits {
		if _, err := s.create("recent_visits", map[string]any{
			"user":       s.id(visit.owner),
			"path":       visit.path,
			"title":      visit.title,
			"visitCount": 1,
			"visitedAt":  s.hoursAgo(visit.hoursAgo).UTC(),
		}); err != nil {
			return err
		}
	}

	return nil
}

// files writes a folder tree and a few library entries.
//
// The records are metadata only: no bytes are uploaded, so a demonstration does not put
// megabytes into a database somebody may later keep. The entries are marked with a note
// saying so, which is what the file list will show.
func (s *Seeder) files() error {
	folders := []struct {
		key, name, parent string
	}{
		{"folder.products", "Product images", ""},
		{"folder.audio", "Audio", "folder.products"},
		{"folder.travel", "Travel", "folder.products"},
		{"folder.documents", "Documents", ""},
	}

	for i, folder := range folders {
		fields := map[string]any{
			"name":      folder.name,
			"owner":     s.id("user.alice"),
			"scope":     "shared",
			"status":    "active",
			"sortOrder": (i + 1) * 10,
		}
		if folder.parent != "" {
			fields["parent"] = s.id(folder.parent)
		}
		rec, err := s.create("file_folders", fields)
		if err != nil {
			return err
		}
		s.remember(folder.key, rec)
	}

	return nil
}
