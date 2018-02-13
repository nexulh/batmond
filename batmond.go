package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/0xAX/notificator"
	"github.com/distatus/battery"
	"github.com/nightlyone/lockfile"
)

var (
	minDelay  int64
	verbose   bool
	critLevel int
	iconSize  int

	running        bool
	retryBatteries int
)

func init() {
	flag.IntVar(&critLevel, "critlevel", 15, "battery level (%) below which critical notifications should be shown")
	flag.Int64Var(&minDelay, "delay", 30, "minimum delay (in seconds) between notifications")
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
	flag.IntVar(&iconSize, "iconSize", 48, "icon size")
	flag.Parse()
}

func main() {
	lfilepath := filepath.Join(os.Getenv("HOME"), ".batmond", ".lock")
	lfile, err := lockfile.New(lfilepath)
	if err != nil {
		fmt.Printf("Could not initialize lockfile: %s\n", err)
		return
	}
	if err := lfile.TryLock(); err != nil {
		fmt.Printf("Could not aquire lockfile (%s): %s\n", lfilepath, err)
		return
	}
	defer lfile.Unlock()

	intSig := make(chan os.Signal)

	if verbose == true {
		fmt.Println("Battery Monitor running")
	}

	bm := BatteryMonitor{
		notificationDelay: time.Second * time.Duration(minDelay),
	}

	if verbose == true {
		tn := TextNotifier{}
		bm.notifiers = append(bm.notifiers, tn)
	}

	nn := NotificationNotifier{
		notifier: notificator.New(notificator.Options{
			DefaultIcon: fmt.Sprintf(".batmond/battery_%d.jpg", iconSize),
			AppName:     "Battery Monitor"}),
	}
	bm.notifiers = append(bm.notifiers, nn)

	signal.Notify(intSig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	running = true
	for running {
		select {
		case <-intSig:
			running = false
		case <-time.Tick(time.Second * 1):
			bm.Update()
		}
	}
}

type BatteryMonitor struct {
	lastNotification  time.Time
	notificationDelay time.Duration
	lastBatteryState  *battery.Battery

	notifiers []Notifier
}

func (bm *BatteryMonitor) Update() {
	batteries, err := battery.GetAll()
	if err != nil {
		return
	}
	retryBatteries++

	if len(batteries) < 1 && retryBatteries > 5 {
		fmt.Println("Could not find any batteries, exiting")
		running = false
	}
	retryBatteries = 0

	for _, b := range batteries {
		if bm.shouldReset(*b) {
			bm.Reset(*b)
			bm.Notify(*b)
		}
		if bm.shouldNotify(*b) {
			bm.Notify(*b)
		}
	}
}

func (bm *BatteryMonitor) Reset(b battery.Battery) {
	bm.lastBatteryState = &b
}

func (bm *BatteryMonitor) Notify(b battery.Battery) {
	percent := b.Current / b.Full * 100
	msg := fmt.Sprintf("%s at %.1f%%", b.State, percent)
	for _, notifier := range bm.notifiers {
		if b.Current/b.Full*100 < float64(critLevel) {
			notifier.Critical(msg)
		} else {
			notifier.Print(msg)
		}
	}

	bm.Reset(b)
	bm.lastNotification = time.Now()
}

func (bm *BatteryMonitor) shouldNotify(b battery.Battery) bool {
	if bm.lastBatteryState == nil {
		return true
	}

	notificationOK := time.Now().After(bm.lastNotification.Add(bm.notificationDelay))
	newPercentage := b.Current / b.Full
	oldPercentage := bm.lastBatteryState.Current / bm.lastBatteryState.Full

	if notificationOK && b.State == battery.Discharging && newPercentage < oldPercentage*0.5 {
		return true
	} else if bm.isNewState(b) {
		return true
	}

	return false
}

func (bm *BatteryMonitor) isNewState(b battery.Battery) bool {
	if bm.lastBatteryState == nil {
		return true
	}
	return b.State != bm.lastBatteryState.State
}

func (bm *BatteryMonitor) shouldReset(b battery.Battery) bool {
	if bm.lastBatteryState == nil {
		return true
	}
	if b.State == battery.Discharging && b.Current > (bm.lastBatteryState.Current+0.01) {
		return true
	}

	return false
}

type Notifier interface {
	Print(s string)
	Critical(s string)
}

type TextNotifier struct{}

func (tn TextNotifier) Print(s string) {
	fmt.Printf("Battery: %s\n", s)
}

func (tn TextNotifier) Critical(s string) {
	tn.Print(s)
}

type NotificationNotifier struct {
	notifier *notificator.Notificator
}

func (nf NotificationNotifier) _print(s string, critical bool) {
	if nf.notifier == nil {
		fmt.Println("NotificationNotifier: NIL notifier")
		return
	}

	msgLevel := notificator.UR_NORMAL
	if critical == true {
		msgLevel = notificator.UR_CRITICAL
	}

	nf.notifier.Push("Battery", fmt.Sprintf("%s", s), "", msgLevel)
}

func (nf NotificationNotifier) Print(s string) {
	nf._print(s, false)
}

func (nf NotificationNotifier) Critical(s string) {
	nf._print(s, true)
}
