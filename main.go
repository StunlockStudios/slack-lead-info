package main

import (
	"fmt"
	"os"

	"strings"

	"github.com/StunlockStudios/confluencegoclient"
	"github.com/pkg/errors"

	"net/http"

	"encoding/json"

	"github.com/PuerkitoBio/goquery"
	"github.com/nlopes/slack"
)

type slackUser struct {
	Name     string
	FullName string
}

type featureTeam struct {
	Name    string
	Members []slackUser
	Owner   *slackUser
}

type leadInfo struct {
	Lead   slackUser
	Grunts []slackUser
}

type slackError struct {
	Message string
}

type slackInfo struct {
	FeatureTeams []featureTeam
	Guilds       []featureTeam
	Leads        []leadInfo
	Errors       []slackError
}

func (info *slackInfo) errMsg(msg string) {
	info.Errors = append(info.Errors, slackError{Message: msg})
}

func (info *slackInfo) error(msg string, err error) {
	wrapped := errors.Wrap(err, msg)
	info.Errors = append(info.Errors, slackError{Message: wrapped.Error()})
}

func getUser(userList []slack.User, name string) *slack.User {
	for _, user := range userList {
		if strings.ToLower(user.Name) == strings.ToLower(name) || user.ID == name {
			return &user
		}
	}
	return nil
}

func slackUserToAPI(user slack.User) slackUser {
	return slackUser{
		Name:     user.Name,
		FullName: user.RealName,
	}
}

func main() {
	http.HandleFunc("/", handler)
	http.ListenAndServe(os.Args[1], nil)
}
func handler(w http.ResponseWriter, r *http.Request) {
	encoder := json.NewEncoder(w)
	if r.URL.Query().Get("pretty") != "" {
		encoder.SetIndent("", "    ")
	}
	encoder.Encode(getSlackInfo())
}
func getSlackInfo() (slackInfo slackInfo) {
	confluenceAuth := confluence.BasicAuth(os.Args[4], os.Args[5])
	confluenceAPI, err := confluence.NewWiki(os.Args[3], confluenceAuth)
	if err != nil {
		slackInfo.error("Failed to auth with confluence", err)
		return
	}
	expand := []string{"body.storage"}
	content, err := confluenceAPI.GetContent("12159063", expand)
	if err != nil {
		slackInfo.error("Failed to get confluence lead assignment page", err)
		return
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content.Body.Storage.Value))
	if err != nil {
		slackInfo.error("Failed to create a goquery document from the confluence content: "+content.Body.Storage.Value, err)
		return
	}
	selection := doc.Find("tr")

	type leadAssignment struct {
		RealName      string
		SlackName     string
		LeadRealName  string
		LeadSlackName string
	}
	var leadAssignmentTable []leadAssignment
	var leadAssignments = map[string][]string{}
	selection.Each(func(num int, row *goquery.Selection) {
		name := row.Find("td").Eq(0).Text()
		slackName := row.Find("td").Eq(1).Text()
		lead := row.Find("td").Eq(2).Text()
		if slackName != "" {
			leadAssignmentTable = append(leadAssignmentTable, leadAssignment{
				RealName:     name,
				SlackName:    slackName,
				LeadRealName: lead,
			})
		}
	})

	for index, assignment := range leadAssignmentTable {
		for _, leadSearch := range leadAssignmentTable {
			if leadSearch.RealName == assignment.LeadRealName {
				leadAssignmentTable[index].LeadSlackName = leadSearch.SlackName
			}
		}
	}

	for _, assignment := range leadAssignmentTable {
		if assignment.LeadSlackName != "" {
			leadAssignments[assignment.LeadSlackName] = append(leadAssignments[assignment.LeadSlackName], assignment.SlackName)
		}
	}

	token := os.Args[2]
	api := slack.New(token)

	channels, err := api.GetChannels(true)
	if err != nil {
		slackInfo.error("Failed to get slack channels", err)
		return
	}

	userList, err := api.GetUsers()
	if err != nil {
		slackInfo.error("Failed to get slack users", err)
		return
	}

	for lead, grunts := range leadAssignments {
		var leadUser = getUser(userList, lead)
		if leadUser == nil {
			slackInfo.errMsg(fmt.Sprintf("Failed to find lead %s in the slack user list", lead))
			continue
		}
		var leadInfo leadInfo
		leadInfo.Lead = slackUserToAPI(*leadUser)
		for _, grunt := range grunts {
			var gruntUser = getUser(userList, grunt)
			if gruntUser == nil {
				slackInfo.errMsg(fmt.Sprintf("Failed to find grunt %s in the slack user list for lead %s", grunt, lead))
				continue
			}
			leadInfo.Grunts = append(leadInfo.Grunts, slackUserToAPI(*gruntUser))
		}
		slackInfo.Leads = append(slackInfo.Leads, leadInfo)
	}

	for _, channel := range channels {
		if strings.HasPrefix(channel.Name, "f-") || strings.HasPrefix(channel.Name, "g-") {
			var team featureTeam
			team.Name = channel.Name
			tokens := strings.Split(channel.Topic.Value, " ")
			for _, token := range tokens {
				if strings.HasPrefix(token, "@") {
					user := getUser(userList, token[1:])
					if user == nil {
						slackInfo.errMsg(fmt.Sprintf("Failed to find an owner called %s from the channel '%s' with topic '%s'", token[1:], channel.Name, channel.Topic.Value))
						continue
					}
					team.Owner = new(slackUser)
					*team.Owner = slackUserToAPI(*user)
				}
			}
			for _, member := range channel.Members {
				user := getUser(userList, member)
				if user == nil {
					slackInfo.errMsg(fmt.Sprintf("Failed to find a matching user in the slack user list for user %s in channel %s", member, channel.Name))
					continue
				}
				team.Members = append(team.Members, slackUserToAPI(*user))
			}
			if strings.HasPrefix(channel.Name, "f-") {
				slackInfo.FeatureTeams = append(slackInfo.FeatureTeams, team)
			} else if strings.HasPrefix(channel.Name, "g-") {
				slackInfo.Guilds = append(slackInfo.Guilds, team)
			} else {
				slackInfo.errMsg(fmt.Sprintf("Failure in handling prefix of channel %s", channel.Name))
			}
		}
	}
	return slackInfo
}
