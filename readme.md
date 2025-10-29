# ARSourcingBD Telegram Bot (Go)

This is a Go-based Telegram bot that helps you generate B2B social media content for your clothing business, ARSourcingBD.

The bot follows a simple, guided workflow:
1.  You send a product photo.
2.  The bot asks you to select the target platform (e.g., LinkedIn, Instagram).
3.  The bot asks you to select the desired tone (e.g., Professional, Luxury).
4.  The bot asks you to select which services to highlight (e.g., OEM, Bulk).
5.  The bot asks for optional, additional context (you can skip this).
6.  The bot then uses the Google Gemini API to analyze the image and your choices, generating 3 caption options, hashtags, and AI feedback.

## Setup & Running

You need two things to run this bot:
1.  A Telegram Bot Token
2.  A Google Gemini API Key

### 1. Get Telegram Bot Token

1.  Open Telegram and search for the `@BotFather` (the official bot for making bots).
2.  Start a chat with it and send the `/newbot` command.
3.  Follow its instructions. It will ask for a name (e.g., "ARSourcing Helper") and a username (e.g., "ARSourcingBot").
4.  When you are finished, BotFather will give you a **token**. It will look something like `1234567890:ABC-DEF1234ghIkl-zyx57W2v1u1234`.

### 2. Get Gemini API Key

1.  Go to [Google AI Studio](https://aistudio.google.com/).
2.  Sign in and create a new project.
3.  Click on **"Get API key"** and create a new API key.

### 3. Configure Your Environment

1.  Clone or download this project's files into a folder.
2.  In that folder, create a new file named `.env` (you can rename the `.env.example` file).
3.  Open the `.env` file and paste your keys in:

    ```
    TELEGRAM_BOT_TOKEN="YOUR_TELEGRAM_BOT_TOKEN_HERE"
    GEMINI_API_KEY="YOUR_GEMINI_API_KEY_HERE"
    ```

### 4. Run the Bot

1.  Open a terminal or command prompt in the project folder.
2.  Run `go mod tidy` to install the dependencies.
3.  Run `go run .` to start the bot.

Your bot is now running! You can open Telegram, find it by the username you created, and send it a photo to start the process.

