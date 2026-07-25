package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/alpindale/ssh-dashboard/internal"
	"github.com/alpindale/ssh-dashboard/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

func validateInterval(seconds float64) time.Duration {
	if seconds < 0.01 || seconds > 3600 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func buildModel(hosts []internal.SSHHost, requestedHosts []string, interval time.Duration) (ui.Model, error) {
	if len(hosts) == 0 {
		return ui.Model{}, fmt.Errorf("no hosts found in SSH config")
	}

	if len(requestedHosts) > 0 {
		selectedHosts := make([]internal.SSHHost, 0, len(requestedHosts))
		hostMap := make(map[string]internal.SSHHost)

		for _, host := range hosts {
			hostMap[host.Name] = host
		}

		for _, requestedName := range requestedHosts {
			host, found := hostMap[requestedName]
			if !found {
				return ui.Model{}, fmt.Errorf("host '%s' not found in SSH config", requestedName)
			}
			selectedHosts = append(selectedHosts, host)
		}

		return ui.InitialModelWithHosts(hosts, selectedHosts, interval), nil
	}

	return ui.InitialModel(hosts, interval), nil
}

func runInteractiveSSH(host string) error {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("error finding ssh: %w", err)
	}

	cmd := exec.Command(sshPath, host)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error executing ssh: %w", err)
	}

	return nil
}

func main() {
	var showVersion bool

	flag.Usage = func() {
		// HACK: make it look like python's argparse
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] [HOST...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  -n, --interval float  Update interval in seconds (default: 5, or SSH_DASHBOARD_INTERVAL env var)\n")
		fmt.Fprintf(os.Stderr, "  -v, --version         Show version information\n")
		fmt.Fprintf(os.Stderr, "  -h, --help            Show this help message\n")
		fmt.Fprintf(os.Stderr, "\nArguments:\n")
		fmt.Fprintf(os.Stderr, "  HOST...               One or more hostnames from SSH config to connect to directly\n")
		fmt.Fprintf(os.Stderr, "                        Example: ssh-dashboard myHost myOtherHost\n")
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n")
		fmt.Fprintf(os.Stderr, "  SSH_DASHBOARD_TIMEOUT Connection timeout in seconds (default: 10)\n")
	}

	var updateIntervalVal float64
	flag.Float64Var(&updateIntervalVal, "n", 0, "")
	flag.Float64Var(&updateIntervalVal, "interval", 0, "")
	flag.BoolVar(&showVersion, "v", false, "")
	flag.BoolVar(&showVersion, "version", false, "")
	flag.Parse()

	requestedHosts := flag.Args()

	if showVersion {
		fmt.Printf("ssh-dashboard version %s\n", internal.FullVersion())
		fmt.Printf("  git commit: %s\n", internal.GitCommit)
		fmt.Printf("  build date: %s\n", internal.BuildDate)
		fmt.Printf("  git tag:    %s\n", internal.GitTag)
		os.Exit(0)
	}

	updateInterval := &updateIntervalVal

	interval := 5 * time.Second

	if *updateInterval > 0 {
		if validated := validateInterval(*updateInterval); validated > 0 {
			interval = validated
		}
	} else if envInterval := os.Getenv("SSH_DASHBOARD_INTERVAL"); envInterval != "" {
		if seconds, err := strconv.ParseFloat(envInterval, 64); err == nil {
			if validated := validateInterval(seconds); validated > 0 {
				interval = validated
			}
		}
	}

	for {
		hosts, err := internal.ParseSSHConfig("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing SSH config: %v\n", err)
			os.Exit(1)
		}

		model, err := buildModel(hosts, requestedHosts, interval)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())
		finalModel, err := p.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
			os.Exit(1)
		}

		m, ok := finalModel.(ui.Model)
		if !ok {
			return
		}

		sshHost := m.GetSSHOnExit()
		if sshHost == "" {
			return
		}

		selectedHostNames := m.GetSelectedHostNames()
		if len(selectedHostNames) > 0 {
			requestedHosts = selectedHostNames
		}

		if err := runInteractiveSSH(sshHost); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
		}
	}
}
