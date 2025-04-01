package handlers

const (
	WelcomeMessage = "Welcome to the CA Scraper Bot! Start exploring safe crypto projects."
	ErrorMessage   = "Oops! Something went wrong. Please try again."
)

func GenerateWelcomeMessage() string {
	return WelcomeMessage
}

func GenerateErrorMessage() string {
	return ErrorMessage
}
