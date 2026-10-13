package demo

// organisation and accounts are the people side of the dataset.
//
// They come first because everything else points at an account: an order belongs to
// somebody, a membership is held by somebody, and a demonstration with orders and no
// customers is a set of orphaned screens.

// organisation writes the departments, positions and staff records.
func (s *Seeder) organisation() error {
	departments := []struct {
		key, code, name string
	}{
		{"dept.support", "SUP", "Customer support"},
		{"dept.warehouse", "WHS", "Warehouse"},
		{"dept.marketing", "MKT", "Marketing"},
		{"dept.finance", "FIN", "Finance"},
	}

	for i, dept := range departments {
		rec, err := s.create("departments", map[string]any{
			"name":      dept.name,
			"code":      dept.code,
			"sortOrder": (i + 1) * 10,
			"status":    "active",
		})
		if err != nil {
			return err
		}
		s.remember(dept.key, rec)
	}

	positions := []struct {
		key, code, name string
	}{
		{"pos.agent", "AGT", "Support agent"},
		{"pos.picker", "PCK", "Warehouse picker"},
		{"pos.marketer", "MKT", "Marketing specialist"},
	}

	for i, position := range positions {
		rec, err := s.create("positions", map[string]any{
			"name":      position.name,
			"code":      position.code,
			"sortOrder": (i + 1) * 10,
			"status":    "active",
		})
		if err != nil {
			return err
		}
		s.remember(position.key, rec)
	}

	return nil
}

// accounts writes the example customers and staff.
//
// The email prefix is what marks them: a reset removes these and nobody else, because
// `users` is on the untouchable list and only rows carrying the marker are ever deleted.
func (s *Seeder) accounts() error {
	people := []struct {
		key, email, name, department, position string
		customer                               bool
	}{
		{"user.alice", "demo.alice@example.com", "Alice Chen", "dept.support", "pos.agent", false},
		{"user.bob", "demo.bob@example.com", "Bob Nakamura", "dept.warehouse", "pos.picker", false},
		{"user.carol", "demo.carol@example.com", "Carol Whitfield", "dept.marketing", "pos.marketer", false},
		{"user.dan", "demo.dan@example.com", "Dan Okafor", "dept.finance", "", false},

		{"user.emma", "demo.emma@example.com", "Emma Lindqvist", "", "", true},
		{"user.frank", "demo.frank@example.com", "Frank Moreau", "", "", true},
		{"user.grace", "demo.grace@example.com", "Grace Adeyemi", "", "", true},
		{"user.hugo", "demo.hugo@example.com", "Hugo Santos", "", "", true},
		{"user.iris", "demo.iris@example.com", "Iris Kowalski", "", "", true},
	}

	for _, person := range people {
		user, err := s.create("users", map[string]any{
			"email":           person.email,
			"password":        "demo-password-please-change",
			"passwordConfirm": "demo-password-please-change",
			"name":            person.name,
			"status":          "active",
			"verified":        true,
		})
		if err != nil {
			return err
		}
		s.remember(person.key, user)

		if person.customer {
			continue
		}

		// A staff record links the account to a department and a position, and an
		// administrator profile is what lets it sign in at all.
		if _, err := s.create("employees", map[string]any{
			"user":       user.Id,
			"name":       person.name,
			"employeeNo": "EMP-" + user.Id[:6],
			"department": s.id(person.department),
			"position":   s.id(person.position),
			"status":     "active",
			"hiredAt":    s.daysAgo(400).UTC(),
		}); err != nil {
			return err
		}
	}

	return nil
}
