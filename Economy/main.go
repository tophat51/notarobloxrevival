// Mercury Economy service

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	c "github.com/TwiN/go-color"
	gonanoid "github.com/matoous/go-nanoid/v2"
)

func randId() (id string) {
	id, _ = gonanoid.Generate("0123456789abcdefghijklmnopqrstuvwxyz", 15)
	return
}

func Log(txt string) {
	// I HATE GO DATE FORMATTI- actually, F#'s isn't any better
	fmt.Println(time.Now().Format("2006/01/02, 15:04:05 "), txt)
}

func Assert(err error, txt string) {
	// so that I don't have to write this every time
	if err != nil {
		Log(c.InRed(txt+": ") + err.Error())
		os.Exit(1)
	}
}

type (
	// uint64 is overkill? idgaf
	userNumber uint64
	currency   uint64
	asset      uint64
)

const (
	filepath = "../data/ledger" // jsonl file

	Micro currency = 1
	Milli          = 1e3 * Micro
	Unit           = 1e6 * Micro // standard unit
	Kilo           = 1e3 * Unit
	Mega           = 1e6 * Unit
	Giga           = 1e9 * Unit
	Tera           = 1e12 * Unit // uint64 means ~18 tera is the economy limit

	// Target Currency per User, the economy size will try to be this * user count (len(balances))
	// By "user", I mean every user who has ever transacted with the economy
	// If I'm correct, the stipend and fee should change if the CCU is more than 10% off from this
	TCU         = float64(100 * Unit)
	baseStipend = float64(10 * Unit)
	baseFee     = 0.1
	stipendTime = 12 * 60 * 60 * 1000
)

func toReadable(c currency) string {
	return fmt.Sprintf("%d.%06d unit", c/Unit, c%Unit)
}

// For now, transaction outputs are overkill
// Since fees are stored as a separate value and are burned, I can't see a reason for them to exist for now
// UTXOs lmao
type SentTx struct {
	From       userNumber
	To         userNumber
	Amount     currency
	Link, Note string // Transaction links might be a bit of an ass backwards concept for now but ion care
	Returns    []asset
}
type Tx struct {
	SentTx
	Fee  currency // we ?could? store the fee as a percentage of the amount instead, unclear atm if it's worth it
	Time uint64
	Id   string
}

type SentMint struct {
	To     userNumber
	Amount currency
	Note   string
}
type Mint struct {
	SentMint
	Time uint64
	Id   string
}

type SentBurn struct {
	From       userNumber
	Amount     currency
	Note, Link string
	Returns    []asset
}
type Burn struct {
	SentBurn
	Time uint64
	Id   string
}

var (
	file         *os.File
	balances     = map[userNumber]currency{}
	prevStipends = map[userNumber]uint64{}
)

func validateTx(sent SentTx, fee currency) (e error) {
	if sent.Amount == 0 {
		e = fmt.Errorf("transaction must have an amount")
	} else if sent.From == 0 {
		e = fmt.Errorf("transaction must have a sender")
	} else if sent.To == 0 {
		e = fmt.Errorf("transaction must have a recipient")
	} else if sent.From == sent.To {
		e = fmt.Errorf("circular transaction: %d -> %d", sent.From, sent.To)
	} else if sent.Note == "" {
		e = fmt.Errorf("transaction must have a note")
	} else if sent.Link == "" {
		e = fmt.Errorf("transaction must have a link")
	} else if total := sent.Amount + fee; total > balances[sent.From] {
		e = fmt.Errorf("insufficient balance: balance was %s, at least %s is required", toReadable(balances[sent.From]), toReadable(total))
	}
	return
}

func validateMint(sent SentMint) (e error) {
	if sent.Amount == 0 {
		e = fmt.Errorf("mint must have an amount")
	} else if sent.To == 0 {
		e = fmt.Errorf("mint must have a recipient")
	} else if sent.Note == "" {
		e = fmt.Errorf("mint must have a note")
	}
	return
}

func validateBurn(sent SentBurn) (e error) {
	if sent.Amount == 0 {
		e = fmt.Errorf("burn must have an amount")
	} else if sent.From == 0 {
		e = fmt.Errorf("burn must have a sender")
	} else if sent.Amount > balances[sent.From] {
		e = fmt.Errorf("insufficient balance: balance was %s, at least %s is required", toReadable(balances[sent.From]), toReadable(sent.Amount))
	} else if sent.Note == "" {
		e = fmt.Errorf("burn must have a note")
	} else if sent.Link == "" {
		e = fmt.Errorf("burn must have a link")
	}
	return
}

func economySize() (size currency) {
	for _, v := range balances {
		size += v
	}
	return
}

// Current Currency per User
func CCU() float64 {
	users := len(balances)
	if users == 0 {
		return 0 // Division by zero causes overflowz
	}
	return float64(economySize()) / float64(users)
}

// If the economy is too small, stipends will increase
// If the economy is near or above desired size, stipends will be baseStipend
func currentStipend() currency {
	return currency(max((TCU-CCU()+baseStipend)/2, baseStipend))
}

// If the economy is too large, fees will increase
// If the economy is near or below desired size, fees will be baseFee
func currentFee() float64 {
	return max((1+(CCU()*0.9-TCU)/TCU*4)*baseFee, baseFee)
}

func readTx(txType string, remaining string) {
	switch txType {
	case "Transaction":
		var tx Tx
		Assert(json.Unmarshal([]byte(remaining), &tx), "Failed to decode transaction from ledger")

		if tx.Amount+tx.Fee > balances[tx.From] {
			fmt.Println("Invalid transaction in ledger")
			os.Exit(1)
		}
		balances[tx.From] -= tx.Amount + tx.Fee
		balances[tx.To] += tx.Amount
	case "Mint":
		var mint Mint
		Assert(json.Unmarshal([]byte(remaining), &mint), "Failed to decode mint from ledger")

		balances[mint.To] += mint.Amount
		if mint.Note == "Stipend" {
			prevStipends[mint.To] = mint.Time
		}
	case "Burn":
		var burn Burn
		Assert(json.Unmarshal([]byte(remaining), &burn), "Failed to decode burn from ledger")

		if burn.Amount > balances[burn.From] {
			fmt.Println("Invalid burn in ledger")
			os.Exit(1)
		}
		balances[burn.From] -= burn.Amount
	default:
		Log(c.InRed("Unknown transaction type in ledger"))
	}
}

func updateBalances() {
	bytes, err := io.ReadAll(file)
	Assert(err, "Failed to read from ledger")

	lines := strings.Split(string(bytes), "\n")
	for _, line := range lines[:len(lines)-1] { // remove last empty line
		parts := strings.SplitN(line, " ", 2) // split line at first space, with the transaction type being the first part
		readTx(parts[0], parts[1])
	}
}

func appendEvent(e any, eType string) error {
	file.WriteString(eType + " ") // Lol good luck error handling this
	return json.NewEncoder(file).Encode(e)
}

func transact(sent SentTx) error {
	fee := currency(float64(sent.Amount) * currentFee())
	if err := validateTx(sent, fee); err != nil {
		return err
	} else if err := appendEvent(
		Tx{sent, fee, uint64(time.Now().UnixMilli()), randId()},
		"Transaction",
	); err != nil {
		return err
	}

	// successfully written
	balances[sent.From] -= sent.Amount + fee
	balances[sent.To] += sent.Amount
	return nil
}

func mint(sent SentMint, time uint64) error {
	if err := validateMint(sent); err != nil {
		return err
	} else if err := appendEvent(Mint{sent, time, randId()}, "Mint"); err != nil {
		return err
	}

	// successfully written
	balances[sent.To] += sent.Amount
	return nil
}

func burn(sent SentBurn) error {
	if err := validateBurn(sent); err != nil {
		return err
	} else if err := appendEvent(Burn{sent, uint64(time.Now().UnixMilli()), randId()}, "Burn"); err != nil {
		return err
	}

	// successfully written
	balances[sent.From] -= sent.Amount
	return nil
}

func stipend(to userNumber) error {
	time := uint64(time.Now().UnixMilli())
	if err := mint(SentMint{to, currentStipend(), "Stipend"}, time); err != nil {
		return err
	}

	prevStipends[to] = time
	return nil
}

func currentFeeRoute(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, currentFee())
}

func currentStipendRoute(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, currentStipend())
}

func balanceRoute(w http.ResponseWriter, r *http.Request) {
	var user userNumber

	if _, err := fmt.Scanf(r.PathValue("id"), "%d", &user); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fmt.Fprint(w, balances[user])
}

func transactRoute(w http.ResponseWriter, r *http.Request) {
	var sentTx SentTx

	if err := json.NewDecoder(r.Body).Decode(&sentTx); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if err := transact(sentTx); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	Log(c.InGreen(fmt.Sprintf("Transaction successful  %d -[%s]-> %d", sentTx.From, toReadable(sentTx.Amount), sentTx.To)))
}

func mintRoute(w http.ResponseWriter, r *http.Request) {
	var sentMint SentMint

	if err := json.NewDecoder(r.Body).Decode(&sentMint); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if err := mint(sentMint, uint64(time.Now().UnixMilli())); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	Log(c.InGreen(fmt.Sprintf("Mint successful         %d <-[%s]-", sentMint.To, toReadable(sentMint.Amount))))
}

func burnRoute(w http.ResponseWriter, r *http.Request) {
	var sentBurn SentBurn

	if err := json.NewDecoder(r.Body).Decode(&sentBurn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if err := burn(sentBurn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	Log(c.InGreen(fmt.Sprintf("Burn successful         %d -[%s]->", sentBurn.From, toReadable(sentBurn.Amount))))
}

func stipendRoute(w http.ResponseWriter, r *http.Request) {
	var to userNumber

	if _, err := fmt.Scanf(r.PathValue("id"), "%d", &to); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if prevStipends[to]+stipendTime > uint64(time.Now().UnixMilli()) {
		http.Error(w, "Next stipend not available yet", http.StatusBadRequest)
		return
	} else if err := stipend(to); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	Log(c.InGreen(fmt.Sprintf("Stipend successful      %d", to)))
}

func main() {
	Log(c.InYellow("Loading ledger..."))
	// create the file if it dont exist
	var err error
	file, err = os.OpenFile(filepath, os.O_CREATE|os.O_APPEND, 0o644)
	Assert(err, "Failed to open ledger")
	defer file.Close()
	updateBalances()

	println("User count    ", len(balances))
	println("Economy size  ", toReadable(economySize()))
	println("CCU           ", toReadable(currency(CCU())))
	println("TCU           ", toReadable(currency(TCU)))
	println("Fee percentage", int(currentFee()*100))
	println("Stipend size  ", toReadable(currentStipend()))

	router := http.NewServeMux()

	router.HandleFunc("GET /currentFee", currentFeeRoute)
	router.HandleFunc("GET /currentStipend", currentStipendRoute)
	router.HandleFunc("GET /balance/{id}", balanceRoute)
	router.HandleFunc("POST /transact", transactRoute)
	router.HandleFunc("POST /mint", mintRoute)
	router.HandleFunc("POST /burn", burnRoute)
	router.HandleFunc("POST /stipend/{id}", stipendRoute)

	Log(c.InGreen("~ Economy service is up on port 2009 ~"))
	http.ListenAndServe(":2009", router) // 03/Jan/2009 Chancellor on brink of second bailout for banks
}