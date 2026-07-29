// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"net/http"
	"strings"
)

// ---- Member data types for templates ----

type membersListData struct {
	pageBase
	Group     CachedGroup
	Members   []GroupMember
	IsLeader  bool
	InviteErr string
}

type memberErrorData struct {
	Error string
}

// ---- Member handlers ----

// handleMembersList renders the member management page for a group.
// GET /groups/{name}/members
func (s *Server) handleMembersList(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "group name required", http.StatusBadRequest)
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	members, _ := s.store.ListMembers(group.GroupKey)

	// Check if the viewer is an authenticated leader for this group.
	leaderKey, ok := s.isAuthenticated(r)
	isLeader := ok && leaderKey == group.GroupKey

	s.renderPage(w, "members", membersListData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
			MetaDesc:  "Members of " + group.DisplayName,
		},
		Group:    group,
		Members:  members,
		IsLeader: isLeader,
	})
}

// handleMemberInvite handles the HTMX POST to invite a member by email.
// POST /groups/{name}/members/invite
func (s *Server) handleMemberInvite(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		s.renderFragment(w, "member_error", memberErrorData{Error: "missing group name"})
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "group not found"})
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	role := strings.TrimSpace(r.FormValue("role"))

	if err := validateString("email", email, 1, 256); err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: err.Error()})
		return
	}

	// Validate role — default to "member" if invalid.
	var memberRole MemberRole
	switch role {
	case "leader":
		memberRole = RoleLeader
	case "co-leader":
		memberRole = RoleCoLeader
	case "member":
		memberRole = RoleMember
	default:
		memberRole = RoleMember
	}

	// Check if member already exists.
	existing, err := s.store.GetMember(group.GroupKey, email)
	if err == nil {
		// Already a member — update role.
		if err := s.store.UpdateMemberRole(group.GroupKey, email, memberRole); err != nil {
			s.renderFragment(w, "member_error", memberErrorData{Error: "failed to update member role"})
			return
		}
		existing.Role = memberRole
		s.renderMemberListFragment(w, r, group, existing)
		return
	}

	// Add new member.
	m := GroupMember{
		GroupKey:    group.GroupKey,
		UserEmail:   email,
		DisplayName: displayName,
		Role:        memberRole,
	}
	if err := s.store.AddMember(m); err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "failed to add member"})
		return
	}

	// Create an invitation record.
	inviteToken := generateToken()
	_ = s.store.CreateInvite(MemberInvite{
		Token:       inviteToken,
		GroupKey:     group.GroupKey,
		InviteeEmail: email,
		InvitedBy:    "leader",
	})

	s.renderMemberListFragment(w, r, group, m)
}

// handleMemberRoleChange handles the HTMX POST to change a member's role.
// POST /groups/{name}/members/{id}/role
func (s *Server) handleMemberRoleChange(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id := r.PathValue("id")
	if name == "" || id == "" {
		s.renderFragment(w, "member_error", memberErrorData{Error: "missing parameters"})
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "group not found"})
		return
	}

	// The {id} path value is the member's email (used as the member identifier).
	email := id

	member, err := s.store.GetMember(group.GroupKey, email)
	if err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "member not found"})
		return
	}

	role := strings.TrimSpace(r.FormValue("role"))
	var memberRole MemberRole
	switch role {
	case "leader":
		memberRole = RoleLeader
	case "co-leader":
		memberRole = RoleCoLeader
	case "member":
		memberRole = RoleMember
	default:
		s.renderFragment(w, "member_error", memberErrorData{Error: "invalid role"})
		return
	}

	if err := s.store.UpdateMemberRole(group.GroupKey, email, memberRole); err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "failed to update role"})
		return
	}

	member.Role = memberRole
	s.renderMemberListFragment(w, r, group, member)
}

// handleMemberRemove handles the HTMX POST to remove a member.
// POST /groups/{name}/members/{id}/remove
func (s *Server) handleMemberRemove(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	id := r.PathValue("id")
	if name == "" || id == "" {
		s.renderFragment(w, "member_error", memberErrorData{Error: "missing parameters"})
		return
	}

	group, err := s.store.GetGroupByCanonicalName(name)
	if err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "group not found"})
		return
	}

	email := id

	// Verify the member exists in this group.
	_, err = s.store.GetMember(group.GroupKey, email)
	if err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "member not found"})
		return
	}

	if err := s.store.RemoveMember(group.GroupKey, email); err != nil {
		s.renderFragment(w, "member_error", memberErrorData{Error: "failed to remove member"})
		return
	}

	// Return the updated member list fragment.
	members, _ := s.store.ListMembers(group.GroupKey)
	leaderKey, ok := s.isAuthenticated(r)
	isLeader := ok && leaderKey == group.GroupKey

	s.renderFragment(w, "members_list", membersListData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		Group:    group,
		Members:  members,
		IsLeader: isLeader,
	})
}

// renderMemberListFragment renders the updated member list fragment with the
// updated member highlighted.
func (s *Server) renderMemberListFragment(w http.ResponseWriter, r *http.Request, group CachedGroup, updatedMember GroupMember) {
	members, _ := s.store.ListMembers(group.GroupKey)
	leaderKey, ok := s.isAuthenticated(r)
	isLeader := ok && leaderKey == group.GroupKey

	s.renderFragment(w, "members_list", membersListData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		Group:    group,
		Members:  members,
		IsLeader: isLeader,
	})
}

// ---- Member template helpers ----

// memberRoleLabel returns a human-readable label for a member role.
func memberRoleLabel(role MemberRole) string {
	switch role {
	case RoleLeader:
		return "Leader"
	case RoleCoLeader:
		return "Co-Leader"
	case RoleMember:
		return "Member"
	default:
		return "Member"
	}
}

// memberRoleBadgeClass returns the CSS badge class for a member role.
func memberRoleBadgeClass(role MemberRole) string {
	switch role {
	case RoleLeader:
		return "badge-accent"
	case RoleCoLeader:
		return "badge-warning"
	case RoleMember:
		return "badge-neutral"
	default:
		return "badge-neutral"
	}
}

// memberRoleString returns the raw string value of a MemberRole for
// use in template comparisons.
func memberRoleString(role MemberRole) string {
	return string(role)
}

