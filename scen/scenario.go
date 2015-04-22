package scen

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"text/template"

	"code.google.com/p/go-uuid/uuid"

	_ "github.com/gonum/blas/native"
	"github.com/gonum/matrix/mat64"
	_ "github.com/mxk/go-sqlite/sqlite3"
	"github.com/rwcarlsen/cyan/post"
)

// Facility represents a cyclus agent prototype that could be built by the
// optimizer.
type Facility struct {
	Proto string
	// Cap is the total Power output capacity of the facility.
	Cap float64
	// The lifetime of the facility (in timesteps). The lifetime must also
	// be specified manually (consistent with this value) in the prototype
	// definition in the cyclus input template file.
	Life int
	// BuildAfter is the time step after which this facility type can be built.
	// -1 for never available, and 0 for always available.
	BuildAfter int
	// FracOfProto names a prototype that build fractions of this prototype
	// are a portion of.
	FracOfProtos []string
}

// Alive returns whether or not a facility built at the specified time is
// still operating/active at t.
func (f *Facility) Alive(built, t int) bool { return Alive(built, t, f.Life) }

// Available returns true if the facility type can be built at time t.
func (f *Facility) Available(t int) bool {
	return t >= f.BuildAfter && f.BuildAfter >= 0
}

type Build struct {
	Time  int
	Proto string
	N     int
	Life  int
	fac   Facility
}

// Alive returns whether or not the facility is still operabing/active at t.
func (b Build) Alive(t int) bool { return Alive(b.Time, t, b.Lifetime()) }

func (b Build) Lifetime() int {
	if b.Life > 0 {
		return b.Life
	}
	return b.fac.Life
}

// Alive returns whether or not a facility with the given lifetime and built
// at the specified time is still operating/active at t.
func Alive(built, t, life int) bool {
	return built <= t && (built+life >= t || life <= 0)
}

type Scenario struct {
	// SimDur is the simulation duration in timesteps (months)
	SimDur int
	// BuildOffset is the number of timesteps after simulation start at which
	// deployments actually begin.  This allows facilities and other initial
	// conditions to be set up and run before the deploying begins.
	BuildOffset int
	// TrailingDur is the number of timesteps of the simulation duration that
	// are reserved for wind-down - no new deployments will be made.
	TrailingDur int
	// CyclusTmpl is the path to the text templated cyclus input file.
	CyclusTmpl string
	// BuildPeriod is the number of timesteps between timesteps in which
	// facilities are deployed
	BuildPeriod int
	// NuclideCost represents the waste cost per kg material per time step for
	// each nuclide in the entire simulation (repository's exempt).
	NuclideCost map[string]float64
	// Discount represents the nominal annual discount rate (including
	// inflation) for the simulation.
	Discount float64
	// Facs is a list of facilities that could be built and associated
	// parameters relevant to the optimization objective.
	Facs []Facility
	// MinPower is a series of min deployed power capacity requirements that
	// must be maintained for each build period.
	MinPower []float64
	// MaxPower is a series of max deployed power capacity requirements that
	// must be maintained for each build period.
	MaxPower []float64
	// Builds holds the set of build schedule values for all agents in the
	// scenario.  This can be used to specify initial condition deployments.
	Builds []Build
	// Addr is the location of the cyclus simulation execution server.  An
	// empty string "" indicates that simulations will run locally.
	Addr string
	// File is the name of the scenario file. This is for internal use and
	// does not need to be filled out by the user.
	File string
	// Handle is used internally and does not need to be specified by the
	// user.
	Handle string
}

func (s *Scenario) reactors() []Facility {
	rs := []Facility{}
	for _, fac := range s.Facs {
		if fac.Cap > 0 {
			rs = append(rs, fac)
		}
	}
	return rs
}

func (s *Scenario) notreactors() []Facility {
	fs := []Facility{}
	for _, fac := range s.Facs {
		if fac.Cap == 0 {
			fs = append(fs, fac)
		}
	}
	return fs
}

func (s *Scenario) nvars() int { return s.nvarsPerPeriod() * s.nperiods() }

func (s *Scenario) nvarsPerPeriod() int {
	numFacVars := len(s.Facs) - 1
	numPowerVars := 1
	return numFacVars + numPowerVars
}

func (s *Scenario) periodFacOrder() (varfacs []Facility, implicitreactor Facility) {
	facs := []Facility{}
	for _, fac := range s.reactors()[1:] {
		facs = append(facs, fac)
	}
	for _, fac := range s.notreactors() {
		facs = append(facs, fac)
	}
	return facs, s.reactors()[0]
}

// TransformVars takes a sequence of input variables for the scenario and
// transforms them into a set of prototype/facility deployments. The sequence
// of the vars follows this pattern: fac1_t1, fac1_t2, ..., fac1_tn, fac2_t1,
// ..., facm_t1, facm_t2, ..., facm_tn.
//
// The first reactor type variable represents the total fraction of new built
// power capacity satisfied by that reactor on the given time step.  For each
// subsequent reactor type (except the last), the variables represent the
// fraction of the remaining power capacity satisfied by that reactor type
// (e.g. the third reactor type's variable can be used to calculate its
// fraction like this (1-(react1frac + (1-react1frac) * react2frac)) *
// react3frac).  The last reactor type fraction is simply the remainining
// unsatisfied power capacity.
func (s *Scenario) TransformVars(vars []float64) (map[string][]Build, error) {
	err := s.Validate()
	if err != nil {
		return nil, err
	} else if len(vars) != s.nvars() {
		return nil, fmt.Errorf("wrong number of vars: want %v, got %v", s.nvars(), len(vars))
	}

	builds := map[string][]Build{}
	for _, b := range s.Builds {
		builds[b.Proto] = append(builds[b.Proto], b)
	}

	varfacs, implicitreactor := s.periodFacOrder()
	caperror := map[string]float64{}
	for i, t := range s.periodTimes() {
		minpow := s.MinPower[i]
		maxpow := s.MaxPower[i]
		currpower := s.powercap(builds, t)
		powervar := vars[i*s.nvarsPerPeriod()]

		toterr := 0.0
		for _, caperr := range caperror {
			toterr += caperr
		}
		shouldhavepower := currpower + toterr
		fmt.Println(shouldhavepower)

		captobuild := math.Max(minpow-shouldhavepower, 0)
		powerrange := maxpow - (shouldhavepower + captobuild)
		captobuild += powervar * powerrange

		// handle reactor builds
		reactorfrac := 0.0
		j := 1 // skip j = 0 which is the power cap variable
		for j = 1; j < s.nvarsPerPeriod(); j++ {
			val := vars[s.BuildPeriod*i+j]
			fac := varfacs[j]
			if fac.Cap > 0 {
				facfrac := (1 - reactorfrac) * val
				reactorfrac += facfrac

				caperr := caperror[fac.Proto]
				wantcap := facfrac*captobuild + caperr
				nbuild := int(math.Max(0, math.Floor(wantcap/fac.Cap+0.5)))
				caperror[fac.Proto] = wantcap - float64(nbuild)*fac.Cap

				builds[fac.Proto] = append(builds[fac.Proto], Build{
					Time:  t,
					Proto: fac.Proto,
					N:     nbuild,
					fac:   fac,
				})
			} else {
				// done processing reactors (except last one)
				break
			}
		}

		// handle last (implicit) reactor
		fac := implicitreactor
		facfrac := (1 - reactorfrac)

		caperr := caperror[fac.Proto]
		wantcap := facfrac*captobuild + caperr
		nbuild := int(math.Max(0, math.Floor(wantcap/fac.Cap+0.5)))
		caperror[fac.Proto] = wantcap - float64(nbuild)*fac.Cap

		builds[fac.Proto] = append(builds[fac.Proto], Build{
			Time:  t,
			Proto: fac.Proto,
			N:     nbuild,
			fac:   fac,
		})

		// handle other facilities
		for ; j < s.nvarsPerPeriod(); j++ {
			facfrac := vars[s.BuildPeriod*i+j]
			fac := varfacs[j]

			caperr := caperror[fac.Proto]
			haven := float64(s.naliveproto(builds, t, fac.Proto))
			needn := facfrac*float64(s.naliveproto(builds, t, fac.FracOfProtos...)) + caperr
			wantn := math.Max(0, needn-haven)
			nbuild := int(math.Max(0, math.Floor(wantn+0.5)))
			caperror[fac.Proto] = wantn - float64(nbuild)

			builds[fac.Proto] = append(builds[fac.Proto], Build{
				Time:  t,
				Proto: fac.Proto,
				N:     nbuild,
				fac:   fac,
			})
		}
	}

	return builds, nil
}

func (s *Scenario) naliveproto(facs map[string][]Build, t int, protos ...string) int {
	count := 0
	for _, proto := range protos {
		builds := facs[proto]
		for _, b := range builds {
			if b.Alive(t) {
				count++
			}
		}
	}
	return count
}

func (s *Scenario) powercap(builds map[string][]Build, t int) float64 {
	pow := 0.0
	for _, buildsproto := range builds {
		for _, b := range buildsproto {
			if b.Alive(t) {
				pow += b.fac.Cap * float64(b.N)
			}
		}
	}
	return pow
}

// Validate returns an error if the scenario is ill-configured.
func (s *Scenario) Validate() error {
	if min, max := len(s.MinPower), len(s.MaxPower); min != max {
		return fmt.Errorf("MaxPower length %v != MinPower length %v", max, min)
	}

	np := s.nperiods()
	lmin := len(s.MinPower)
	if np != lmin {
		return fmt.Errorf("number power constraints %v != number build periods %v", lmin, np)
	}

	protos := map[string]Facility{}
	for _, fac := range s.Facs {
		protos[fac.Proto] = fac
	}

	for _, p := range s.Builds {
		fac, ok := protos[p.Proto]
		if !ok {
			return fmt.Errorf("param prototype '%v' is not defined in Facs", p.Proto)
		}
		p.fac = fac
	}

	return nil
}

func (s *Scenario) Load(fname string) error {
	if s == nil {
		s = &Scenario{}
	}
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, s); err != nil {
		if serr, ok := err.(*json.SyntaxError); ok {
			line, col := findLine(data, serr.Offset)
			return fmt.Errorf("%s:%d:%d: %v", fname, line, col, err)
		}
		return err
	}

	s.File = fname
	if len(s.Builds) == 0 {
		s.Builds = make([]Build, s.nvars())
	}
	return s.Validate()
}

func (s *Scenario) GenCyclusInfile() ([]byte, error) {
	if s.Handle == "" {
		s.Handle = "none"
	}

	var buf bytes.Buffer
	tmpl := s.CyclusTmpl
	t := template.Must(template.ParseFiles(tmpl))

	err := t.Execute(&buf, s)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (s *Scenario) Run(stdout, stderr io.Writer) (dbfile string, simid []byte, err error) {
	// generate cyclus input file and run cyclus
	ui := uuid.NewRandom()
	cycin := ui.String() + ".cyclus.xml"
	cycout := ui.String() + ".sqlite"

	data, err := s.GenCyclusInfile()
	if err != nil {
		return "", nil, err
	}
	err = ioutil.WriteFile(cycin, data, 0644)
	if err != nil {
		return "", nil, err
	}

	cmd := exec.Command("cyclus", cycin, "-o", cycout)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}

	if err := cmd.Run(); err != nil {
		return "", nil, err
	}

	// post process cyclus output db
	db, err := sql.Open("sqlite3", cycout)
	if err != nil {
		return "", nil, err
	}
	defer db.Close()

	simids, err := post.Process(db)
	if err != nil {
		return "", nil, err
	}

	return cycout, simids[0], nil
}

func (s *Scenario) VarNames() []string {
	nperiods := s.nperiods()
	names := make([]string, s.nvars())
	for f := range s.Facs {
		for n, t := range s.periodTimes() {
			i := f*nperiods + n
			names[i] = fmt.Sprintf("f%v_t%v", f, t)
		}
	}
	return names
}

func (s *Scenario) LowerBounds() *mat64.Dense {
	return mat64.NewDense(s.nvars(), 1, nil)
}

func (s *Scenario) UpperBounds() *mat64.Dense {
	up := mat64.NewDense(s.nvars(), 1, nil)
	for i := 0; i < s.nvars(); i++ {
		up.Set(i, 0, 1)
	}
	return up
}

func (s *Scenario) timeOf(period int) int {
	return period*s.BuildPeriod + 1 + s.BuildOffset
}

func (s *Scenario) periodOf(time int) int {
	return (time - s.BuildOffset - 1) / s.BuildPeriod
}

func (s *Scenario) periodTimes() []int {
	periods := make([]int, s.nperiods())
	for i := range periods {
		periods[i] = s.timeOf(i)
	}
	return periods
}

func (s *Scenario) nperiods() int {
	return (s.SimDur-s.BuildOffset-s.TrailingDur-2)/s.BuildPeriod + 1
}

func findLine(data []byte, pos int64) (line, col int) {
	line = 1
	buf := bytes.NewBuffer(data)
	for n := int64(0); n < pos; n++ {
		b, err := buf.ReadByte()
		if err != nil {
			panic(err) //I don't really see how this could happen
		}
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return
}
