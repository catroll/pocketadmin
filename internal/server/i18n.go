package server

import (
	"github.com/gin-gonic/gin"

	"pocketadmin/internal/i18n"
	"pocketadmin/internal/rbac"
)

// translate renders a message for a request.
//
// A page's title is passed through it, and the template helper below uses the same
// catalogue. Both go through i18n.T, so a missing message is visible as its key wherever
// it appears rather than as a blank in one place and a key in another.
func (s *Server) translate(c *gin.Context, message string) string {
	if message == "" {
		return ""
	}

	locale := s.locale(c)
	if locale == i18n.Default {
		// Nothing to do for the source language, and this is the path every request
		// takes on an installation that has not chosen one.
		return message
	}

	// A title is an English sentence, so it is looked up as a message. The menu key is
	// tried first because a title and the menu entry for the same screen share a word,
	// and the reverse index covers every other catalogued message.
	if translated := i18n.T(locale, "nav."+menuKeyFor(message)); translated != "nav."+menuKeyFor(message) {
		return translated
	}
	return i18n.Text(locale, message)
}

// menuKeyFor turns a page title into the menu key it is most likely to share.
//
// Titles and menu entries are the same words for the same screens, so a page called
// "Machines" and a menu entry called "Machines" should translate once. Anything without a
// match is left alone.
func menuKeyFor(title string) string {
	for _, key := range i18n.Keys() {
		if len(key) > 4 && key[:4] == "nav." && i18n.T(i18n.Default, key) == title {
			return key[4:]
		}
	}
	return title
}

// translateMenus renders the navigation in a locale.
//
// The titles live in the database because an operator may rename or reorder them, so the
// translation is a lookup at render time rather than a column: a menu row keeps its
// English title as the name an operator recognises, and what is drawn is the message
// under `nav.<name>`. A menu an operator added by hand has no message and keeps the title
// they typed, which is the right answer -- it is their word.
func (s *Server) translateMenus(nodes []rbac.MenuNode, locale i18n.Locale) []rbac.MenuNode {
	if locale == i18n.Default {
		return nodes
	}

	out := make([]rbac.MenuNode, len(nodes))
	for i, node := range nodes {
		out[i] = node

		key := "nav." + node.Name
		if translated := i18n.T(locale, key); translated != key {
			out[i].Title = translated
		}

		if len(node.Children) > 0 {
			out[i].Children = s.translateMenus(node.Children, locale)
		}
	}
	return out
}
