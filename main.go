package main

import (
	"fmt"
	"log"
	"os"

	"strings"

	"github.com/StunlockStudios/confluencegoclient"

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

type slackInfo struct {
	FeatureTeams []featureTeam
	Leads        []leadInfo
}

func getUser(userList []slack.User, name string) *slack.User {
	for _, user := range userList {
		if user.Name == name || user.ID == name {
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
	confluenceAuth := confluence.BasicAuth(os.Args[4], os.Args[5])
	confluenceAPI, err := confluence.NewWiki(os.Args[3], confluenceAuth)
	if err != nil {
		log.Fatal(err)
	}
	expand := []string{"body.storage"}
	content, err := confluenceAPI.GetContent("12159063", expand)
	if err != nil {
		log.Fatal(err)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content.Body.Storage.Value))
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}

	userList, err := api.GetUsers()
	if err != nil {
		log.Fatal(err)
	}

	var slackInfo slackInfo
	for lead, grunts := range leadAssignments {
		var leadUser = getUser(userList, lead)
		if leadUser == nil {
			log.Fatal(fmt.Sprintf("User %s not found", lead))
		}
		var leadInfo leadInfo
		leadInfo.Lead = slackUserToAPI(*leadUser)
		for _, grunt := range grunts {
			var gruntUser = getUser(userList, grunt)
			if gruntUser == nil {
				log.Fatal(fmt.Sprintf("User %s not found", grunt))
			}
			leadInfo.Grunts = append(leadInfo.Grunts, slackUserToAPI(*gruntUser))
		}
		slackInfo.Leads = append(slackInfo.Leads, leadInfo)
	}

	for _, channel := range channels {
		if strings.HasPrefix(channel.Name, "f-") {
			var team featureTeam
			team.Name = channel.Name
			tokens := strings.Split(channel.Topic.Value, " ")
			for _, token := range tokens {
				if strings.HasPrefix(token, "@") {
					user := getUser(userList, token[1:])
					if user == nil {
						log.Fatal(fmt.Sprintf("Could not find user %s", token))
					}
					team.Owner = new(slackUser)
					*team.Owner = slackUserToAPI(*user)
				}
			}
			for _, member := range channel.Members {
				user := getUser(userList, member)
				if user == nil {
					log.Fatal(fmt.Sprintf("Could not find user %s", member))
				}
				team.Members = append(team.Members, slackUserToAPI(*user))
			}
			slackInfo.FeatureTeams = append(slackInfo.FeatureTeams, team)
		}
	}
	json.NewEncoder(w).Encode(slackInfo)
}
