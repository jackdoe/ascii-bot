package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	iq "github.com/rekki/go-query"

	"github.com/gin-gonic/gin"
	analyzer "github.com/rekki/go-query-analyze"
	norm "github.com/rekki/go-query-analyze/normalize"
	"github.com/rekki/go-query-analyze/tokenize"
	index "github.com/rekki/go-query-index"
)

type Art struct {
	id   int
	blob string
	tags []string
}

func (a *Art) Blocks(_qs string) []*Block {
	return []*Block{
		{
			Type: "section",
			Text: &Text{
				Type: "mrkdwn", Text: fmt.Sprintf("```\n%s\n```", strings.Trim(a.blob, "\n")),
			},
		},
	}
}

func (a *Art) Buttons(qs string) *Block {
	return &Block{
		BlockID: "action_123",
		Type:    "actions",
		Elements: []*Element{
			{
				Type: "button",
				Text: &Text{
					Type: "plain_text",
					Text: "Post it!",
				},
				Style:    "primary",
				Value:    fmt.Sprintf("%d/%s", a.id, qs),
				ActionID: "post_it",
			},
			{
				Type: "button",
				Text: &Text{
					Type: "plain_text",
					Text: "Shuffle!",
				},
				ActionID: "shuffle",
				Value:    qs,
			},
		},
	}
}

func (a *Art) IndexableFields() map[string][]string {
	out := map[string][]string{}

	out["blob"] = []string{a.blob}
	out["tags"] = a.tags
	out["match_all"] = []string{"true"}
	return out
}

func toDocuments(in []*Art) []index.Document {
	out := make([]index.Document, len(in))
	for i, d := range in {
		out[i] = index.Document(d)
	}
	return out
}

func GetShinglesAnalyzer() *analyzer.Analyzer {
	andAmp := []norm.Normalizer{
		norm.NewUnaccent(),
		norm.NewLowerCase(),
		norm.NewSpaceBetweenDigits(),
		norm.NewCustom(func(s string) string {
			return strings.Replace(s, "#", " ", -1)
		}),
		norm.NewRemoveNonAlphanumeric(),
		norm.NewTrim(" "),
	}

	indexTokenizer := []tokenize.Tokenizer{
		tokenize.NewWhitespace(),
		tokenize.NewShingles(2),
	}

	return analyzer.NewAnalyzer(
		andAmp,
		index.DefaultSearchTokenizer,
		indexTokenizer,
	)
}

func prepare(root string) []*Art {
	out := []*Art{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".txt") {
			f, err := ioutil.ReadFile(p)
			if err != nil {
				return err
			}
			if len(f) > 3500 {
				// wont fit
				log.Printf("skipping %v, too big: %v", p, len(f))
				return nil
			}
			art := &Art{
				blob: string(f),
				tags: []string{info.Name()},
				id:   len(out),
			}
			out = append(out, art)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}

type PostMessage struct {
	User    string   `json:"user"`
	Channel string   `json:"channel"`
	Blocks  []*Block `json:"blocks,omitempty"`
}

type SlackResponse struct {
	ResponseType    string   `json:"response_type,omitempty"`
	ReplaceOriginal bool     `json:"replace_original,omitempty"`
	DeleteOriginal  bool     `json:"delete_original,omitempty"`
	Blocks          []*Block `json:"blocks,omitempty"`
}

type Text struct {
	Type string `json:"type,omitempty"`
	Text string `json:"text,omitempty"`
}
type Block struct {
	Type     string     `json:"type,omitempty"`
	Text     *Text      `json:"text,omitempty"`
	BlockID  string     `json:"block_id,omitempty"`
	Elements []*Element `json:"elements,omitempty"`
}

type Element struct {
	Type     string `json:"type,omitempty"`
	Style    string `json:"style,omitempty"`
	URL      string `json:"url,omitempty"`
	Value    string `json:"value,omitempty"`
	ActionID string `json:"action_id,omitempty"`
	Text     *Text  `json:"text,omitempty"`
}

func main() {
	root := flag.String("root", "./art", "folder")
	flag.Parse()

	ana := GetShinglesAnalyzer()
	m := index.NewMemOnlyIndex(map[string]*analyzer.Analyzer{
		"blob": ana,
		"tags": ana,
	})

	list := prepare(*root)
	m.Index(toDocuments(list)...)

	r := gin.Default()

	search := func(qs string) *Art {
		q := iq.DisMax(0.1, iq.Or(m.Terms("tags", qs)...), iq.Or(m.Terms("blob", qs)...))

		out := &Art{}
		max := float32(0)
		found := false
		m.Foreach(q, func(did int32, score float32, doc index.Document) {
			score = float32(rand.Int31())
			art := doc.(*Art)
			if score > max {
				out = art
				max = score
			}
			found = true
		})

		if found {
			return out
		}
		return nil
	}

	r.POST("/ascii", func(c *gin.Context) {
		qs := c.PostForm("text")
		art := search(qs)
		if art == nil {
			response := SlackResponse{
				Blocks: []*Block{
					{
						Type: "section",
						Text: &Text{
							Type: "mrkdwn", Text: fmt.Sprintf("```\n%s\n```", "couldnt find anything.... try something else or help me to add more ascii art"),
						},
					},
				},
			}
			c.JSON(200, response)
			return
		}

		response := SlackResponse{
			ResponseType: "in_channel",
			Blocks:       art.Blocks(qs),
		}
		c.JSON(200, &response)
	})

	r.Run()
}
