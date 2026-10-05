// 2026 Jan Provaznik (jan@provaznik.pro)
//

package main

import "os"
import "fmt"
import "flag"
import "time"
import "github.com/jan-provaznik/sus"
import "github.com/NVIDIA/go-nvml/pkg/nvml"

var upperDrawLimit float64 = 150
var lowerDrawLimit float64 = 10
var matchDrawLimit float64 = 1

func main() {
	interval := flag.Duration("t", time.Second, "Monitoring interval")

	flag.Float64Var(&upperDrawLimit, "u", 105.0, "Maximal power draw per wire (W).")
	flag.Float64Var(&matchDrawLimit, "m", 0.75, "Maximal mismatch ratio.")
	flag.Parse()

	if upperDrawLimit > 150 || upperDrawLimit < 1 {
		fmt.Println("Invalid upperDrawLimit. Restrict to 1 <= value < 150.")
		os.Exit(1)
	}

	if matchDrawLimit > 1 || matchDrawLimit < 0 {
		fmt.Println("Invalid matchDrawLimit. Restrict to 0 <= value < 1.")
		os.Exit(1)
	}

	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		fmt.Println("nvmlInit failed")
		os.Exit(1)
	}
	defer nvml.Shutdown()

	list, err := sus.FindAstralDevices()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if len(list) < 1 {
		fmt.Println("Could not find any compatible devices. Exiting.")
		os.Exit(0)
	}

	for index, device := range list {
		fmt.Printf("Detected device (%d) identified by (%s)\n",
			index, device.Identifier())
	}
	fmt.Println()

	for {
		for index, device := range list {
			err := deviceMonitor(index, device)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}
		time.Sleep(*interval)
	}
}

func deviceMonitor(index int, device sus.AstralDevice) error {
	// ... load, as reported via asus interface
	pins, err := sus.ReadAstralDevicePins(device)
	if err != nil {
		return err
	}

	// ... calculate statistics
	totalDraw := 0.0
	upperDraw := 0.0
	lowerDraw := 1e6

	for _, pin := range pins {
		value := pin.Drawing()
		if value > upperDraw {
			upperDraw = value
		}
		if value < lowerDraw {
			lowerDraw = value
		}
		totalDraw = totalDraw + value
	}

	// ... calculate draw match
	matchDraw := lowerDraw / upperDraw

	// ... emergency actions (pin overload)
	if upperDraw > upperDrawLimit {
		fmt.Printf("Device (%d) identified by (%s)\n",
			index, device.Identifier())
		fmt.Printf("... detected overload %.1f (limit %.1f)\n",
			upperDraw, upperDrawLimit)

		limit, err := sus.LimitAstralDeviceLoad(device)
		if err != nil {
			return err
		}

		fmt.Printf("... limiting power draw to %.1f W\n",
			limit)
	}

	// ... emergency actions (pin mismatch min-max draw)
	if lowerDraw > lowerDrawLimit {
		if matchDraw < matchDrawLimit {
			fmt.Printf("Device (%d) identified by (%s)\n",
				index, device.Identifier())
			fmt.Printf("... detected mismatch %.2f (limit %.2f)\n",
				matchDraw, matchDrawLimit)

			limit, err := sus.LimitAstralDeviceFreq(device)
			if err != nil {
				return err
			}

			fmt.Printf("... attempting to limit device frequency to %d MHz\n",
				limit)
		}
	}

	return nil
}
