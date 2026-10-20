package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"manage/internal/access"
	"manage/internal/store"
)

// The tools of the MCP server that change things, for a token its owner
// allowed to: each goes through the same service, rights, e-mails and
// history as the pages. They are marked so that the assistant's client
// asks before calling them, as it sees fit.

func ptr[T any](v T) *T { return &v }

// changes marks a tool that changes something but deletes nothing;
// deletes, one that may take something away.
var (
	changes = &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}
	deletes = &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}
)

type slotIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition"`
	Pole    string `json:"pole" jsonschema:"the slug of the pole"`
	Slot    string `json:"slot" jsonschema:"the id of the slot, from planning"`
}

type peopleOnSlotIn struct {
	slotIn
	Emails []string `json:"emails" jsonschema:"the e-mail addresses of the people to sign up"`
}

type personOnSlotIn struct {
	slotIn
	Email string `json:"email" jsonschema:"the e-mail address of the person"`
}

type poleMembersIn struct {
	Edition string   `json:"edition" jsonschema:"the slug of the edition"`
	Pole    string   `json:"pole" jsonschema:"the slug of the pole"`
	Emails  []string `json:"emails" jsonschema:"e-mail addresses; someone without an account gets one"`
}

type poleMemberIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition"`
	Pole    string `json:"pole" jsonschema:"the slug of the pole"`
	Email   string `json:"email" jsonschema:"the e-mail address of the member"`
}

type noticeIn struct {
	Edition   string `json:"edition" jsonschema:"the slug of the edition"`
	Pole      string `json:"pole,omitempty" jsonschema:"the slug of a pole, to write to its members; none for the whole edition (coordination)"`
	Slot      string `json:"slot,omitempty" jsonschema:"the id of a slot of that pole, to write to the people on it"`
	Message   string `json:"message" jsonschema:"the notice, as the volunteers will read it"`
	SendEmail bool   `json:"send_email,omitempty" jsonschema:"also e-mail it to them"`
}

type slotFieldsIn struct {
	StartsAt       string   `json:"starts_at,omitempty" jsonschema:"start, as YYYY-MM-DDTHH:MM, event time (Paris)"`
	EndsAt         string   `json:"ends_at,omitempty" jsonschema:"end, as YYYY-MM-DDTHH:MM, event time (Paris)"`
	Capacity       int      `json:"capacity,omitempty" jsonschema:"how many people the slot wants"`
	Title          *string  `json:"title,omitempty" jsonschema:"a title, empty for none"`
	Place          *string  `json:"place,omitempty" jsonschema:"the name of one of the pole's places, empty for the whole pole"`
	Tasks          []string `json:"tasks,omitempty" jsonschema:"the checklist of a new slot"`
	ConfirmOutside bool     `json:"confirm_outside,omitempty" jsonschema:"create or move the slot even outside the edition's dates"`
}

type createSlotIn struct {
	Edition string `json:"edition" jsonschema:"the slug of the edition"`
	Pole    string `json:"pole" jsonschema:"the slug of the pole"`
	slotFieldsIn
}

type updateSlotIn struct {
	slotIn
	slotFieldsIn
}

type remindIn struct {
	Edition  string `json:"edition" jsonschema:"the slug of the edition"`
	Audience string `json:"audience" jsonschema:"idle (volunteers on no slot yet) or details (on a slot, logistics not answered)"`
}

type doneOut struct {
	Done     string   `json:"done"`
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) mcpWriteTools(srv *mcp.Server, a *access.Actor) {
	mcp.AddTool(srv, &mcp.Tool{Name: "sign_up_people", Annotations: changes,
		Description: "Sign people up on a slot of a pole the person runs, even beyond its places; someone not in the pole joins it. " +
			"Each is told by e-mail."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in peopleOnSlotIn) (*mcp.CallToolResult, doneOut, error) {
			out, err := s.mcpSignUpPeople(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "take_off_slot", Annotations: deletes,
		Description: "Take someone off a slot of a pole the person runs. They are told by e-mail, and those waiting of the free place."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in personOnSlotIn) (*mcp.CallToolResult, doneOut, error) {
			v, sl, err := s.mcpSlot(ctx, a, in.slotIn)
			if err == nil {
				var who *store.Person
				if who, err = s.store.PersonByEmail(ctx, store.NormalizeEmail(in.Email)); err == nil {
					err = s.takeOff(ctx, a, v, sl, who)
				}
			}

			return nil, doneOut{Done: "taken off the slot"}, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "add_members", Annotations: changes,
		Description: "Add people to a pole the person runs, by e-mail address."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in poleMembersIn) (*mcp.CallToolResult, doneOut, error) {
			out, err := s.mcpAddMembers(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "remove_member", Annotations: deletes,
		Description: "Take a member out of a pole the person runs, and off its slots. They are told by e-mail."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in poleMemberIn) (*mcp.CallToolResult, doneOut, error) {
			v, err := s.mcpPole(ctx, a, in.Edition, in.Pole)
			if err == nil {
				var who *store.Person
				if who, err = s.store.PersonByEmail(ctx, store.NormalizeEmail(in.Email)); err == nil {
					err = s.removeMember(ctx, a, v, who)
				}
			}

			return nil, doneOut{Done: "taken out of the pole"}, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "post_notice", Annotations: changes,
		Description: "Post a notice on the page of an edition (coordination), of a pole or of a slot (whoever runs the pole), " +
			"and on request e-mail it to whom it concerns."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in noticeIn) (*mcp.CallToolResult, doneOut, error) {
			out, err := s.mcpNotice(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "create_slot", Annotations: changes,
		Description: "Create a slot in a pole the person runs."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createSlotIn) (*mcp.CallToolResult, slotOut, error) {
			out, err := s.mcpCreateSlot(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "update_slot", Annotations: changes,
		Description: "Change a slot of a pole the person runs: only the fields given. The people on it are told when it moves."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updateSlotIn) (*mcp.CallToolResult, slotOut, error) {
			out, err := s.mcpUpdateSlot(ctx, a, in)

			return nil, out, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "delete_slot", Annotations: deletes,
		Description: "Delete a slot of a pole the person runs. The people on it, or waiting for it, are told by e-mail."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in slotIn) (*mcp.CallToolResult, doneOut, error) {
			v, sl, err := s.mcpSlot(ctx, a, in)
			if err == nil {
				_, err = s.deleteSlots(ctx, a, v, []*store.Slot{sl})
			}

			return nil, doneOut{Done: "slot deleted"}, toolErr(err)
		})

	mcp.AddTool(srv, &mcp.Tool{Name: "remind", Annotations: changes,
		Description: "E-mail the volunteers of an edition yet to pick a slot (idle), or those on a slot who have not answered " +
			"the logistics (details). For its coordination."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in remindIn) (*mcp.CallToolResult, doneOut, error) {
			e, err := s.mcpEdition(ctx, a, in.Edition)
			if err != nil {
				return nil, doneOut{}, toolErr(err)
			}

			n, err := s.remind(ctx, a, e, in.Audience)

			return nil, doneOut{Done: plural("en", "mcp.reminded", n)}, toolErr(err)
		})
}

// mcpPole loads a pole as the actor sees it.
func (s *Server) mcpPole(ctx context.Context, a *access.Actor, edition, pole string) (*poleView, error) {
	e, err := s.mcpEdition(ctx, a, edition)
	if err != nil {
		return nil, err
	}

	p, err := s.store.GetPoleBySlug(ctx, e.ID, pole)
	if err != nil {
		return nil, err
	}

	return s.poleViewOf(ctx, a, e, p)
}

// mcpSlot loads a slot of a pole as the actor sees it.
func (s *Server) mcpSlot(ctx context.Context, a *access.Actor, in slotIn) (*poleView, *store.Slot, error) {
	v, err := s.mcpPole(ctx, a, in.Edition, in.Pole)
	if err != nil {
		return nil, nil, err
	}

	sl, err := s.store.GetSlotByUID(ctx, v.Event.ID, in.Slot, 0)
	if err != nil {
		return nil, nil, err
	}

	if sl.PoleID != v.Pole.ID {
		return nil, nil, errNotFound
	}

	return v, sl, nil
}

// lang is the language of the accounts the actor makes: theirs.
func lang(a *access.Actor) string {
	if a.Person != nil && a.Person.Lang != "" {
		return a.Person.Lang
	}

	return "fr"
}

func (s *Server) mcpSignUpPeople(ctx context.Context, a *access.Actor, in peopleOnSlotIn) (doneOut, error) {
	v, sl, err := s.mcpSlot(ctx, a, in.slotIn)
	if err != nil {
		return doneOut{}, err
	}

	if len(in.Emails) == 0 {
		return doneOut{}, errBadInput
	}

	// Someone without an account gets one, if the actor may bring people
	// into the pole: the service checks the rest.
	var people []*store.Person

	for _, email := range in.Emails {
		email = store.NormalizeEmail(email)

		p, err := s.store.PersonByEmail(ctx, email)

		switch {
		case errors.Is(err, store.ErrNotFound) && validEmail(email) && a.CanAddMembers(v.Pole):
			if p, err = s.store.EnsureVolunteer(ctx, email, "", "", lang(a)); err != nil {
				return doneOut{}, err
			}
		case err != nil:
			return doneOut{}, err
		}

		people = append(people, p)
	}

	done, err := s.signUpOthers(ctx, a, v, sl, people)
	if err != nil {
		return doneOut{}, err
	}

	out := doneOut{Done: plural("en", "mcp.signed_up", len(done))}

	for _, p := range done {
		if p.Also != "" {
			out.Warnings = append(out.Warnings, p.Person.Label()+" is also on another slot at that time: "+p.Also)
		}
	}

	if sl, err := s.store.GetSlot(ctx, sl.ID, 0); err == nil && sl.Filled > sl.Capacity {
		out.Warnings = append(out.Warnings, "the slot is now overbooked")
	}

	return out, nil
}

func (s *Server) mcpAddMembers(ctx context.Context, a *access.Actor, in poleMembersIn) (doneOut, error) {
	v, err := s.mcpPole(ctx, a, in.Edition, in.Pole)
	if err != nil {
		return doneOut{}, err
	}

	var people []newMember

	for _, email := range in.Emails {
		if email = store.NormalizeEmail(email); !validEmail(email) {
			return doneOut{}, errBadInput
		}

		people = append(people, newMember{Email: email})
	}

	added, err := s.addMembers(ctx, a, v, people, lang(a))

	return doneOut{Done: plural("en", "mcp.members_added", len(added))}, err
}

func (s *Server) mcpNotice(ctx context.Context, a *access.Actor, in noticeIn) (doneOut, error) {
	e, err := s.mcpEdition(ctx, a, in.Edition)
	if err != nil {
		return doneOut{}, err
	}

	sc := noticeScope{Event: e}

	if in.Pole != "" {
		v, err := s.mcpPole(ctx, a, in.Edition, in.Pole)
		if err != nil {
			return doneOut{}, err
		}

		sc.Pole = v.Pole

		if in.Slot != "" {
			if _, sc.Slot, err = s.mcpSlot(ctx, a, slotIn{Edition: in.Edition, Pole: in.Pole, Slot: in.Slot}); err != nil {
				return doneOut{}, err
			}
		}
	} else if in.Slot != "" {
		return doneOut{}, errBadInput
	}

	sent, err := s.postNotice(ctx, a, sc, in.Message, in.SendEmail)
	if err != nil || !in.SendEmail {
		return doneOut{Done: "notice posted"}, err
	}

	return doneOut{Done: plural("en", "mcp.notice_sent", sent)}, nil
}

// draftOf fills a slot draft from the fields given, over the slot's own.
func (s *Server) draftOf(ctx context.Context, v *poleView, in slotFieldsIn, d slotDraft) (slotDraft, error) {
	at := func(text string, into *time.Time) error {
		if text == "" {
			return nil
		}

		t, err := time.ParseInLocation("2006-01-02T15:04", text, paris)
		if err != nil {
			return errBadInput
		}

		*into = t

		return nil
	}

	if err := at(in.StartsAt, &d.StartsAt); err != nil {
		return d, err
	}

	if err := at(in.EndsAt, &d.EndsAt); err != nil {
		return d, err
	}

	if in.Capacity != 0 {
		d.Capacity = in.Capacity
	}

	if in.Title != nil {
		d.Title = *in.Title
	}

	if in.Place != nil {
		d.Location = ""

		if name := strings.TrimSpace(*in.Place); name != "" {
			places, err := s.store.ListLocations(ctx, v.Pole.ID)
			if err != nil {
				return d, err
			}

			for _, l := range places {
				if strings.EqualFold(l.Name, name) {
					d.Location = l.UID
				}
			}

			if d.Location == "" {
				return d, refuse("mcp.err.unknown_place", name)
			}
		}
	}

	return d, nil
}

func slotOf(sl *store.Slot) slotOut {
	return slotOut{ID: sl.UID, StartsAt: clockIn(sl.StartsAt), EndsAt: clockIn(sl.EndsAt), Place: sl.LocationName, Title: sl.Title,
		Capacity: sl.Capacity, Filled: sl.Filled, People: []personRef{}, TasksDone: sl.TaskDone, TasksTotal: sl.TaskCount}
}

func (s *Server) mcpCreateSlot(ctx context.Context, a *access.Actor, in createSlotIn) (slotOut, error) {
	v, err := s.mcpPole(ctx, a, in.Edition, in.Pole)
	if err != nil {
		return slotOut{}, err
	}

	d, err := s.draftOf(ctx, v, in.slotFieldsIn, slotDraft{Capacity: 1})
	if err != nil {
		return slotOut{}, err
	}

	_, typed, err := s.createSlots(ctx, a, v, d, slotSeries{Tasks: in.Tasks}, in.ConfirmOutside)
	if err != nil {
		return slotOut{}, err
	}

	// The slot just made: the one at that place and time.
	slots, err := s.store.ListSlots(ctx, v.Pole.ID, 0)
	if err != nil {
		return slotOut{}, err
	}

	for _, sl := range slots {
		if sl.LocationID == typed.LocationID && sl.StartsAt.Equal(typed.StartsAt) && sl.EndsAt.Equal(typed.EndsAt) {
			return slotOf(sl), nil
		}
	}

	return slotOf(typed), nil
}

func (s *Server) mcpUpdateSlot(ctx context.Context, a *access.Actor, in updateSlotIn) (slotOut, error) {
	v, sl, err := s.mcpSlot(ctx, a, in.slotIn)
	if err != nil {
		return slotOut{}, err
	}

	current := slotDraft{Title: sl.Title, Capacity: sl.Capacity, StartsAt: sl.StartsAt, EndsAt: sl.EndsAt}

	if sl.LocationID != 0 {
		places, err := s.store.ListLocations(ctx, v.Pole.ID)
		if err != nil {
			return slotOut{}, err
		}

		for _, l := range places {
			if l.ID == sl.LocationID {
				current.Location = l.UID
			}
		}
	}

	d, err := s.draftOf(ctx, v, in.slotFieldsIn, current)
	if err != nil {
		return slotOut{}, err
	}

	_, after, err := s.updateSlot(ctx, a, v, sl, d, in.ConfirmOutside)
	if err != nil {
		return slotOut{}, err
	}

	return slotOf(after), nil
}
