package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// --- Structs for API Payloads and Responses ---

const geminiAPIURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash-preview-09-2025:generateContent?key="

// GeminiRequest is the top-level structure for a Gemini API call.
type GeminiRequest struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction SystemInstruction `json:"systemInstruction"`
	GenerationConfig  GenerationConfig  `json:"generationConfig,omitempty"`
}

// Content represents the user's content (e.g., text and image).
type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// Part can be text, inline data (image), or function call.
type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

// InlineData holds the base64-encoded image data.
type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// SystemInstruction guides the model's persona and task.
type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

// GenerationConfig specifies output format (e.g., JSON).
type GenerationConfig struct {
	ResponseMimeType string  `json:"responseMimeType,omitempty"`
	ResponseSchema   *Schema `json:"responseSchema,omitempty"`
}

// Schema defines the expected JSON output structure.
type Schema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required"`
}

// Property defines a single field in the JSON schema.
type Property struct {
	Type  string `json:"type"`
	Items *struct {
		Type string `json:"type"`
	} `json:"items,omitempty"`
}

// GeminiResponse is the raw response from the API.
type GeminiResponse struct {
	Candidates []struct {
		Content Content `json:"content"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

// --- Specific Structs for Our Bot ---

// GeneratedContent holds the final, parsed data we want.
type GeneratedContent struct {
	Captions []string
	Hashtags []string
	Feedback string
}

// APIJSONResponse is the struct that matches our JSON schema.
type APIJSONResponse struct {
	Caption1 string   `json:"caption1"`
	Caption2 string   `json:"caption2"`
	Caption3 string   `json:"caption3"`
	Hashtags []string `json:"hashtags"`
}

// schemaForCaptions defines the JSON we expect for the main content.
var schemaForCaptions = &Schema{
	Type: "OBJECT",
	Properties: map[string]Property{
		"caption1": {Type: "STRING"},
		"caption2": {Type: "STRING"},
		"caption3": {Type: "STRING"},
		"hashtags": {
			Type: "ARRAY",
			Items: &struct {
				Type string `json:"type"`
			}{Type: "STRING"},
		},
	},
	Required: []string{"caption1", "caption2", "caption3", "hashtags"},
}

// --- Main API Call Function ---

// generateContentFromGemini is the main function that calls the Gemini API.
// It's a single, reusable function that can handle both JSON and text requests.
func generateContentFromGemini(apiKey string, requestBody GeminiRequest) (string, error) {
	apiURL := geminiAPIURL + apiKey
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("error marshalling request: %w", err)
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error creating new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error making API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("API Error Response Body: %s", string(body))
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var geminiResponse GeminiResponse
	if err := json.Unmarshal(body, &geminiResponse); err != nil {
		return "", fmt.Errorf("error unmarshalling response: %w", err)
	}

	// Handle blocked prompts
	if geminiResponse.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("prompt was blocked: %s", geminiResponse.PromptFeedback.BlockReason)
	}

	// Extract and return the generated text
	if len(geminiResponse.Candidates) > 0 && len(geminiResponse.Candidates[0].Content.Parts) > 0 {
		return geminiResponse.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("no content found in API response")
}

// --- Bot-Specific Helper Functions ---

// buildCaptionSystemPrompt creates the detailed prompt for the AI.
func buildCaptionSystemPrompt(platform, tone string, services []string, context string) string {
	var platformInstruction string
	switch platform {
	case "Facebook":
		platformInstruction = "Optimize for Facebook: Engaging, slightly longer, encourage comments. Emojis are good."
	case "Instagram":
		platformInstruction = "Optimize for Instagram: Visually descriptive, strong hook. 3-5 relevant emojis."
	case "X":
		platformInstruction = "Optimize for X (Twitter): Concise and punchy (under 280 chars). 2-3 key hashtags."
	case "LinkedIn":
		platformInstruction = "Optimize for LinkedIn: Professional, formal, focus on business value. Minimal/no emojis."
	default:
		platformInstruction = "Optimize for general social media."
	}

	var servicesList string
	if len(services) > 0 {
		servicesList = strings.Join(services, ", ")
	} else {
		servicesList = "our full range of manufacturing services"
	}

	// This is the core "brain" of the AI, taken from our web app.
	systemPrompt := fmt.Sprintf(`You are a professional B2B (business-to-business) marketing copywriter for **AR Sourcing Bangladesh (arsourcingbd)**, a high-quality clothing manufacturer. Your task is to analyze the provided image of a clothing product and generate compelling social media content.
            
**Business Identity:** AR Sourcing Bangladesh (arsourcingbd)
**Target Platform:** %s (%s)
**Desired Tone:** %s
**Services to Highlight:** %s
**Additional Context:** %s

**Gold-Standard Example (Use for tone/style):**
---
Custom-Made for Global Brands
At AR Sourcing Bangladesh, we specialize in manufacturing high-quality women’s shorts...
🧵 What We Offer:
✅ Premium fabric & professional stitching
✅ OEM & Private Label production
...
🌍 From Bangladesh to the world...
📩 Partner with us for your next clothing collection.
#ApparelManufacturer ... #ARsourcingBangladesh ...
---

**Your Task:**
Based on all the above, generate a JSON object with three (3) unique captions and a list of 15 relevant hashtags.
- The captions must follow the style of the example, be tailored to the product image, and incorporate the specified platform, tone, and services.
- Mention "AR Sourcing Bangladesh" or "arsourcingbd" in the captions.
- The hashtags should be a mix of general (#ApparelManufacturer), specific (#WomensShorts), and branded (#ARsourcingBangladesh).
`, platform, platformInstruction, tone, servicesList, context)

	return systemPrompt
}

// buildFeedbackSystemPrompt creates a simpler prompt for image feedback.
func buildFeedbackSystemPrompt() string {
	return "You are a helpful B2B marketing assistant. Analyze the user's product image and provide a single, concise sentence of constructive feedback for its use on social media. Focus on lighting, angle, or professionalism. Be polite."
}

// getB2BContent is the main entry point called by the bot.
// It orchestrates the two API calls to Gemini.
func getB2BContent(apiKey string, photoData []byte, mimeType string, state *userState) (*GeneratedContent, error) {
	base64Image := base64.StdEncoding.EncodeToString(photoData)
	finalContent := GeneratedContent{}

	// --- 1. Generate Captions and Hashtags (JSON Mode) ---
	log.Println("Generating captions and hashtags...")
	captionContext := state.Context
	if captionContext == "" {
		captionContext = "None provided."
	}

	captionPrompt := buildCaptionSystemPrompt(state.Platform, state.Tone, state.Services, captionContext)
	captionRequest := GeminiRequest{
		Contents: []Content{
			{
				Role: "user",
				Parts: []Part{
					{Text: "Analyze this image and generate the B2B content as requested in the system prompt."},
					{InlineData: &InlineData{MimeType: mimeType, Data: base64Image}},
				},
			},
		},
		SystemInstruction: SystemInstruction{
			Parts: []Part{{Text: captionPrompt}},
		},
		GenerationConfig: GenerationConfig{
			ResponseMimeType: "application/json",
			ResponseSchema:   schemaForCaptions,
		},
	}

	jsonResponse, err := generateContentFromGemini(apiKey, captionRequest)
	if err != nil {
		return nil, fmt.Errorf("error generating captions: %w", err)
	}

	var apiJSONResponse APIJSONResponse
	if err := json.Unmarshal([]byte(jsonResponse), &apiJSONResponse); err != nil {
		log.Printf("Failed to unmarshal JSON: %s", jsonResponse)
		return nil, fmt.Errorf("error parsing caption JSON: %w", err)
	}

	finalContent.Captions = []string{apiJSONResponse.Caption1, apiJSONResponse.Caption2, apiJSONResponse.Caption3}
	finalContent.Hashtags = apiJSONResponse.Hashtags

	// --- 2. Generate Image Feedback (Text Mode) ---
	log.Println("Generating AI feedback...")
	feedbackPrompt := buildFeedbackSystemPrompt()
	feedbackRequest := GeminiRequest{
		Contents: []Content{
			{
				Role: "user",
				Parts: []Part{
					{Text: "What's your feedback on this product photo for B2B marketing?"},
					{InlineData: &InlineData{MimeType: mimeType, Data: base64Image}},
				},
			},
		},
		SystemInstruction: SystemInstruction{
			Parts: []Part{{Text: feedbackPrompt}},
		},
	}

	feedbackText, err := generateContentFromGemini(apiKey, feedbackRequest)
	if err != nil {
		log.Printf("Warning: Could not generate AI feedback: %v", err)
		finalContent.Feedback = "Could not generate AI feedback at this time."
	} else {
		finalContent.Feedback = feedbackText
	}

	return &finalContent, nil
}
