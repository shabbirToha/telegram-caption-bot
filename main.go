package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

// --- Structs and State Management ---

// ConversationState defines the steps in the bot's conversation.
type ConversationState int

const (
	StateDefault ConversationState = iota
	StateWaitingForPlatform
	StateWaitingForTone
	StateWaitingForServices
	StateWaitingForContext
)

// userState holds the data for a single user's conversation.
type userState struct {
	State     ConversationState
	PhotoData []byte // Raw image data
	MimeType  string // e.g., "image/jpeg"
	Platform  string
	Tone      string
	Services  []string
	Context   string
	MessageID int // The ID of the message we are editing (e.g., "Please choose...")
}

// Bot holds the API and the state for all users.
type Bot struct {
	api        *tgbotapi.BotAPI
	userStates map[int64]*userState
	mu         sync.Mutex // Mutex to protect userStates map
	geminiKey  string
}

// --- Main Function ---

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, relying on environment variables.")
	}

	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	geminiKey := os.Getenv("GEMINI_API_KEY")

	if telegramToken == "" || geminiKey == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN and GEMINI_API_KEY must be set in .env or environment")
	}

	api, err := tgbotapi.NewBotAPI(telegramToken)
	if err != nil {
		log.Panic(err)
	}

	api.Debug = false
	log.Printf("Authorized on account %s", api.Self.UserName)

	bot := &Bot{
		api:        api,
		userStates: make(map[int64]*userState),
		geminiKey:  geminiKey,
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := api.GetUpdatesChan(u)

	// --- NEW: Start the bot logic in a separate goroutine ---
	// This lets the bot run its long-pollyng loop
	// while the main thread runs the HTTP server for health checks.
	go func() {
		// Listen for updates
		for update := range updates {
			if update.CallbackQuery != nil {
				bot.handleCallbackQuery(update.CallbackQuery)
			} else if update.Message != nil {
				if update.Message.Photo != nil && len(update.Message.Photo) > 0 { // Added safety check
					bot.handlePhoto(update.Message)
				} else if update.Message.IsCommand() {
					bot.handleCommand(update.Message)
				} else {
					bot.handleMessage(update.Message)
				}
			}
		}
	}()

	// --- NEW: Start a simple HTTP server for health checks ---
	// Hosting platforms like Render.com require the app to bind to a port
	// and respond to HTTP requests to be considered "healthy".
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Bot is alive!")
	})

	// Get the port from the environment (required by hosting platforms)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default port for local testing
	}

	log.Printf("Starting health check server on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Panic(err)
	}
}

// --- State Management Helpers ---

// getState retrieves or creates a state for a user.
func (b *Bot) getState(userID int64) *userState {
	b.mu.Lock()
	defer b.mu.Unlock()

	if state, ok := b.userStates[userID]; ok {
		return state
	}
	// Create a new state
	newState := &userState{State: StateDefault}
	b.userStates[userID] = newState
	return newState
}

// resetState clears a user's state after a job is done or cancelled.
func (b *Bot) resetState(userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// We can just create a new one; old data will be garbage collected
	b.userStates[userID] = &userState{State: StateDefault}
}

// --- Message & Command Handlers ---

func (b *Bot) handleCommand(message *tgbotapi.Message) {
	state := b.getState(message.From.ID)

	switch message.Command() {
	case "start":
		msgText := "Welcome to the ARSourcingBD Content Bot! 👋\n\n" +
			"Please send me a **photo** of your product to get started. I will then guide you through a few questions to generate the perfect social media post."
		b.sendMessage(message.Chat.ID, msgText, nil)
		b.resetState(message.From.ID)
	case "cancel":
		b.resetState(message.From.ID)
		b.sendMessage(message.Chat.ID, "Your previous operation has been cancelled. Send a photo to start over.", nil)
	default:
		b.sendMessage(message.Chat.ID, "I don't know that command. Send /start or a photo.", nil)
	}

	// If the command was sent in the middle of a process, clean up the old inline keyboard
	if state.MessageID != 0 {
		b.removeInlineKeyboard(message.Chat.ID, state.MessageID)
	}
}

func (b *Bot) handlePhoto(message *tgbotapi.Message) {
	userID := message.From.ID
	state := b.getState(userID)

	// Get the largest photo
	// FIX: Removed the incorrect asterisk. message.Photo is a slice, not a pointer.
	photo := message.Photo[len(message.Photo)-1]

	// Download the photo
	photoData, mimeType, err := b.downloadFile(photo.FileID)
	if err != nil {
		log.Printf("Error downloading file: %v", err)
		b.sendMessage(message.Chat.ID, "Sorry, I had trouble downloading your photo. Please try again.", nil)
		return
	}

	// Save data to state
	state.PhotoData = photoData
	state.MimeType = mimeType
	state.State = StateWaitingForPlatform

	// Ask the first question
	msgText := "Great photo! 📸 Now, which platform is this for?"
	msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
	msg.ReplyMarkup = platformKeyboard

	sentMsg, err := b.api.Send(msg)
	if err == nil {
		// Store the message ID so we can edit it later
		state.MessageID = sentMsg.MessageID
	}
}

func (b *Bot) handleMessage(message *tgbotapi.Message) {
	state := b.getState(message.From.ID)

	if state.State == StateWaitingForContext {
		// User sent text, this is their optional context
		state.Context = message.Text
		state.State = StateDefault // Ready to generate

		// Clean up the "Skip" message
		b.removeInlineKeyboard(message.Chat.ID, state.MessageID)

		// Start the generation process
		b.generateContent(message.Chat.ID)
	} else {
		// User sent text out of context
		msgText := "I'm not sure what to do with that. 🤔\n\n" +
			"Please send me a **photo** to start generating content, or /cancel to restart."
		b.sendMessage(message.Chat.ID, msgText, nil)
	}
}

// --- Callback (Button) Handler ---

func (b *Bot) handleCallbackQuery(query *tgbotapi.CallbackQuery) {
	userID := query.From.ID
	state := b.getState(userID)
	data := query.Data

	// Answer the callback to remove the "loading" icon on the button
	b.api.Send(tgbotapi.NewCallback(query.ID, ""))

	switch state.State {
	case StateWaitingForPlatform:
		state.Platform = strings.Split(data, ":")[1]
		state.State = StateWaitingForTone
		b.editMessage(userID, "Got it. And what's the **tone** you're going for?", toneKeyboard)

	case StateWaitingForTone:
		state.Tone = strings.Split(data, ":")[1]
		state.State = StateWaitingForServices
		b.editMessage(userID, "Perfect. Which **services** should I highlight? (Select all that apply, then 'Done')", buildServicesKeyboard(state.Services))

	case StateWaitingForServices:
		if strings.HasPrefix(data, "service:") {
			// User is toggling a service
			service := strings.Split(data, ":")[1]
			// Toggle the service in the state
			var newServices []string
			found := false
			for _, s := range state.Services {
				if s == service {
					found = true
				} else {
					newServices = append(newServices, s)
				}
			}
			if !found {
				newServices = append(newServices, service)
			}
			state.Services = newServices
			// Re-draw the keyboard with the new checkmarks
			b.editMessage(userID, "Perfect. Which **services** should I highlight? (Select all that apply, then 'Done')", buildServicesKeyboard(state.Services))

		} else if data == "control:done_services" {
			// User is done selecting services
			state.State = StateWaitingForContext
			b.editMessage(userID, "Last step! Any **additional context**? (e.g., 'This is for our new sustainable line.')\n\nType your answer or press 'Skip'.", contextKeyboard)
		}

	case StateWaitingForContext:
		if data == "control:skip_context" {
			state.Context = ""                              // Explicitly set as empty
			state.State = StateDefault                      // Ready to generate
			b.removeInlineKeyboard(userID, state.MessageID) // Clean up the "Skip" message
			b.generateContent(userID)
		}
	}
}

// --- Content Generation ---

func (b *Bot) generateContent(userID int64) {
	state := b.getState(userID)

	// 1. Send "thinking" message
	thinkingMsg, _ := b.api.Send(tgbotapi.NewMessage(userID, "Got it! ✨ Analyzing image and your requirements... This might take a moment."))

	// 2. Call Gemini
	content, err := getB2BContent(b.geminiKey, state.PhotoData, state.MimeType, state)
	if err != nil {
		log.Printf("Error generating content: %v", err)
		b.sendMessage(userID, fmt.Sprintf("Oh no! I ran into an error: %s\n\nPlease try again. /cancel", err.Error()), nil)
		b.api.Send(tgbotapi.NewDeleteMessage(userID, thinkingMsg.MessageID)) // Delete "thinking" msg
		b.resetState(userID)
		return
	}

	// 3. Format and send the results
	b.api.Send(tgbotapi.NewDeleteMessage(userID, thinkingMsg.MessageID)) // Delete "thinking" msg

	// --- Send Caption 1 ---
	b.sendMessage(userID, fmt.Sprintf("--- **Option 1** ---\n\n%s", content.Captions[0]), nil)

	// --- Send Caption 2 ---
	b.sendMessage(userID, fmt.Sprintf("--- **Option 2** ---\n\n%s", content.Captions[1]), nil)

	// --- Send Caption 3 ---
	b.sendMessage(userID, fmt.Sprintf("--- **Option 3** ---\n\n%s", content.Captions[2]), nil)

	// --- Send Hashtags & Feedback ---
	hashtagString := ""
	for _, h := range content.Hashtags {
		hashtagString += h + " "
	}

	finalMsg := fmt.Sprintf("👇 **Suggested Hashtags** 👇\n`%s`\n\n", hashtagString)
	finalMsg += fmt.Sprintf("💡 **AI Image Feedback**\n*%s*", content.Feedback)

	msg := tgbotapi.NewMessage(userID, finalMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	// 4. Reset state
	b.resetState(userID)
}

// --- Bot API Helpers ---

// sendMessage is a simple wrapper to send text.
func (b *Bot) sendMessage(userID int64, text string, markup interface{}) {
	msg := tgbotapi.NewMessage(userID, text)
	if markup != nil {
		msg.ReplyMarkup = markup
	}
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

// editMessage updates an existing message with new text and keyboard.
func (b *Bot) editMessage(userID int64, text string, markup tgbotapi.InlineKeyboardMarkup) {
	state := b.getState(userID)
	if state.MessageID == 0 {
		log.Println("Warning: No MessageID to edit")
		b.sendMessage(userID, text, markup) // Fallback to sending a new message
		return
	}

	msg := tgbotapi.NewEditMessageText(userID, state.MessageID, text)
	msg.ReplyMarkup = &markup
	msg.ParseMode = "Markdown"

	if _, err := b.api.Send(msg); err != nil {
		log.Printf("Error editing message, might be unchanged: %v", err)
	}
}

// removeInlineKeyboard removes the buttons from a message.
func (b *Bot) removeInlineKeyboard(userID int64, messageID int) {
	if messageID == 0 {
		return
	}
	edit := tgbotapi.NewEditMessageReplyMarkup(userID, messageID, tgbotapi.InlineKeyboardMarkup{InlineKeyboard: [][]tgbotapi.InlineKeyboardButton{}})
	b.api.Send(edit)
}

// downloadFile downloads a file from Telegram and returns its data.
func (b *Bot) downloadFile(fileID string) ([]byte, string, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, "", err
	}

	fileURL := file.Link(b.api.Token)
	resp, err := http.Get(fileURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("bad status: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	// Get MimeType
	mimeType := http.DetectContentType(data)
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		log.Printf("Warning: Uploaded file is %s, not jpeg/png.", mimeType)
		// We'll try anyway, Gemini is flexible
	}

	return data, mimeType, nil
}

// --- Inline Keyboards (Buttons) ---

var platformKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("LinkedIn", "platform:LinkedIn"),
		tgbotapi.NewInlineKeyboardButtonData("Instagram", "platform:Instagram"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Facebook", "platform:Facebook"),
		tgbotapi.NewInlineKeyboardButtonData("X (Twitter)", "platform:X"),
	),
)

var toneKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Professional", "tone:Professional"),
		tgbotapi.NewInlineKeyboardButtonData("Enthusiastic", "tone:Enthusiastic"),
	),
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Luxury", "tone:Luxury"),
		tgbotapi.NewInlineKeyboardButtonData("Technical", "tone:Technical"),
	),
)

// buildServicesKeyboard dynamically creates the service buttons with checkmarks.
func buildServicesKeyboard(selectedServices []string) tgbotapi.InlineKeyboardMarkup {
	services := map[string]string{
		"OEM":    "OEM / Private Label",
		"Custom": "Custom Branding",
		"Bulk":   "Bulk Manufacturing",
		"Fabric": "Premium Fabric",
	}

	// Helper to check if a service is selected
	isSelected := func(key string) bool {
		for _, s := range selectedServices {
			if s == key {
				return true
			}
		}
		return false
	}

	// Helper to create the button text
	buttonText := func(key, text string) string {
		if isSelected(key) {
			return "✅ " + text
		}
		return text
	}

	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText("OEM", services["OEM"]), "service:OEM"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText("Custom", services["Custom"]), "service:Custom"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText("Bulk", services["Bulk"]), "service:Bulk"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText("Fabric", services["Fabric"]), "service:Fabric"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➡️ Done Selecting ➡️", "control:done_services"),
		),
	)
}

var contextKeyboard = tgbotapi.NewInlineKeyboardMarkup(
	tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Skip This Step", "control:skip_context"),
	),
)
