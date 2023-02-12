package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

func main() {
	apiToken := flag.String("token", os.Getenv("TODOIST_API_TOKEN"), "todoist api token")
	projectName := flag.String("project", "", "project name")
	target := flag.String("target", time.Now().Format("2006/01"), "target YYYY/MM")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ctx := context.Background()

	projectID, err := searchProjectByName(ctx, *apiToken, *projectName)
	if err != nil {
		log.Fatalln(err)
	}

	// todoistのアクティビティログは、今日を0ページ目として取得する必要があるため
	// 指定した年/月が、何ページ目か何ページ目までなのかを計算する
	targetDate, err := time.Parse("2006/01", *target)
	if err != nil {
		log.Fatalln(err)
	}

	// 今日から数えて、指定した年/月の月初（1日）が何周前か計算する
	since := time.Since(targetDate)
	endPage := int(since.Seconds() / 60 / 60 / 24 / 7)
	startPage := endPage - 5 // 1ヶ月最大でも5週間なので開始を5週間前にしたらOK
	if startPage < 0 {
		startPage = 0 // 0スタートなので0以下になったら最初から取得する
	}
	//fmt.Println(startPage, endPage)

	for i := startPage; i <= endPage; i++ {
		// TODO: 1weekで100タスク以上をこなすケースが対応できていない（count > 100だったら offsetを調整して再起的に呼び出す必要がある）
		response, err := getActivityLog(ctx, *apiToken, projectID, i, 0, 100)
		if err != nil {
			log.Fatalln(err)
		}
		//fmt.Printf("total=%d\n", response.Count)

		for _, event := range response.Events {
			if targetDate.Month() != event.EventDate.Month() {
				continue
			}

			fmt.Printf("%s %s\n",
				event.EventDate.Format("2006/01/02 15:04:02"),
				event.ExtraData.Content,
			)
		}
	}
}

type GetProjectsResponse struct {
	Projects []struct {
		IsArchived   bool        `json:"is_archived"`
		Color        string      `json:"color"`
		Shared       bool        `json:"shared"`
		InboxProject bool        `json:"inbox_project"`
		ID           string      `json:"id"`
		Collapsed    bool        `json:"collapsed"`
		ChildOrder   int         `json:"child_order"`
		Name         string      `json:"name"`
		IsDeleted    bool        `json:"is_deleted"`
		ParentID     interface{} `json:"parent_id"`
		ViewStyle    string      `json:"view_style"`
	} `json:"projects"`
	FullSync      bool `json:"full_sync"`
	TempIDMapping struct {
	} `json:"temp_id_mapping"`
	SyncToken string `json:"sync_token"`
}

func searchProjectByName(ctx context.Context, apiToken string, projectName string) (string, error) {
	response, err := getProjects(ctx, apiToken)
	if err != nil {
		return "", fmt.Errorf("get project error: %w", err)
	}

	for _, project := range response.Projects {
		if projectName == project.Name {
			return project.ID, nil
		}
	}

	return "", errors.New("project not exists")
}

const syncGetURL = "https://api.todoist.com/sync/v9/sync"

func getProjects(ctx context.Context, apiToken string) (GetProjectsResponse, error) {
	getURL, err := url.Parse(syncGetURL)
	if err != nil {
		return GetProjectsResponse{}, fmt.Errorf("url parse error: %w", err)
	}

	payload := map[string]interface{}{
		"sync_token":     "*",
		"resource_types": []string{"projects"},
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&payload); err != nil {
		return GetProjectsResponse{}, fmt.Errorf("payload marshal error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, getURL.String(), &buf)
	if err != nil {
		return GetProjectsResponse{}, fmt.Errorf("new request error: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiToken))
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return GetProjectsResponse{}, fmt.Errorf("http request do error: %w", err)
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return GetProjectsResponse{}, fmt.Errorf("http get response read error: %w", err)
	}

	var response GetProjectsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return GetProjectsResponse{}, fmt.Errorf("http get response json unmarshall error: %w", err)
	}

	return response, nil
}

const activityLogGetURL = "https://api.todoist.com/sync/v9/activity/get"

type GetActivityLogResponse struct {
	Events []struct {
		ID              uint64    `json:"id"`
		ObjectType      string    `json:"object_type"`
		ObjectID        string    `json:"object_id"`
		EventType       string    `json:"event_type"`
		EventDate       time.Time `json:"event_date"`
		ParentProjectID string    `json:"parent_project_id"`
		ParentItemID    *string   `json:"parent_item_id"`
		InitiatorID     *string   `json:"initiator_id"`
		ExtraData       struct {
			LastDueDate *time.Time `json:"last_due_date"`
			DueDate     time.Time  `json:"due_date"`
			Content     string     `json:"content"`
			Client      string     `json:"client"`
		} `json:"extra_data,omitempty"`
	} `json:"events"`
	Count int `json:"count"`
}

func getActivityLog(ctx context.Context, apiToken string, projectID string, page int, offset int, limit int) (GetActivityLogResponse, error) {
	getURL, err := url.Parse(activityLogGetURL)
	if err != nil {
		return GetActivityLogResponse{}, fmt.Errorf("url parse error: %w", err)
	}

	params := url.Values{}
	params.Add("event_type", "completed")
	params.Add("parent_project_id", projectID)
	params.Add("page", strconv.Itoa(page))
	params.Add("offset", strconv.Itoa(offset))
	params.Add("limit", strconv.Itoa(limit))
	getURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL.String(), nil)
	if err != nil {
		return GetActivityLogResponse{}, fmt.Errorf("new request error: %w", err)
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", apiToken))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return GetActivityLogResponse{}, fmt.Errorf("http request do error: %w", err)
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return GetActivityLogResponse{}, fmt.Errorf("http get response read error: %w", err)
	}

	var response GetActivityLogResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return GetActivityLogResponse{}, fmt.Errorf("http get response json unmarshall error: %w", err)
	}

	return response, nil
}
