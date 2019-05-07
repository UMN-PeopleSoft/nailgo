package nailgo

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type NailgunConnection struct {
	Conn net.Conn
	Output io.Writer
	Outerr io.Writer
}


func (self *NailgunConnection) sendChunk(chunkType byte, payload []byte) (err error) {
	var header [5]byte

	n := len(payload)
	header[0] = byte((n >> 24) & 0xff)
	header[1] = byte((n >> 16) & 0xff)
	header[2] = byte((n >> 8) & 0xff)
	header[3] = byte((n >> 0) & 0xff)

	header[4] = chunkType

	n, err = self.Conn.Write(header[:])

	if err != nil {
		return
	}

	if n != len(header) {
		log.Panic("Unexpected short-write")
	}

	n, err = self.Conn.Write(payload)
	if err != nil {
		return
	}

	if n != len(payload) {
		log.Panic("Unexpected short-write")
	}

	return nil
}

func (self *NailgunConnection) sendArguments(cmdargs []string) (err error) {
	for _, arg := range cmdargs {
		//if i <= 1 {
			// Unlike nailgun, we always skip the first two args
			// the first is our name
			// the second is the command to run
		//	continue
		//}

		err = self.sendChunk('A', []byte(arg))
		if err != nil {
			return
		}
	}

	return nil
}

func (self *NailgunConnection) sendEnvironment() (err error) {
	for _, env := range os.Environ() {
		err = self.sendChunk('E', []byte(env))
		if err != nil {
			return
		}
	}      
	fileSep := "NAILGUN_FILESEPARATOR=" + strconv.QuoteRune(os.PathListSeparator)
	pathSep := "NAILGUN_PATHSEPARATOR=" + strconv.QuoteRune(os.PathSeparator)
	err = self.sendChunk('E', []byte(fileSep))
	err = self.sendChunk('E', []byte(pathSep))

	return nil
}

func (self *NailgunConnection) sendWorkingDirectory() (err error) {
	cwd, err := filepath.Abs(".")
	if err != nil {
		return
	}

	err = self.sendChunk('D', []byte(cwd))
	if err != nil {
		return
	}

	return nil
}

func (self *NailgunConnection) SendCommand(command string, cmdargs []string) (exitCode int, err error) {
	exitCode = 0

	defer self.close()
   // send arguments to the class
	err = self.sendArguments(cmdargs)
   if err != nil {
		exitCode = 1
		return exitCode, err
	}
	err = self.sendEnvironment()
	err = self.sendWorkingDirectory()
   if err != nil {
		exitCode = 1
		return exitCode, err
	}
	
	//send actual java class to run
	err = self.sendChunk('C', []byte(command))
   if err != nil {
		exitCode = 1
		return exitCode, err
	}

	exitCode, err = self.readFromServer()
	return

}


func (self *NailgunConnection) forwardStdin() (err error) {
	N := 8192
	buffer := make([]byte, N)
	r := os.Stdin

	var n int

	for {
		n, err = r.Read(buffer)
		if err != nil {
			if n == 0 && err == io.EOF {
				break
			}
			return
		}

		err = self.sendChunk('0', buffer[:n])
		if err != nil {
			return
		}
	}

	err = self.sendChunk('.', buffer[0:0])
	if err != nil {
		return
	}

	return nil
}

func (self *NailgunConnection) readFully(buffer []byte) (err error) {
	var n int
	n, err = io.ReadFull(self.Conn, buffer)
	if err != nil {
		return
	}

	if n != len(buffer) {
		return fmt.Errorf("Unexpected short read of response buffer")
		//log.Panic("Unexpected short read")
	}

	return nil
}

func (self *NailgunConnection) readFromServer() (exitCode int, err error) {
	N := 16284
	header := make([]byte, 5, 5)
	buffer := make([]byte, N, N)
   exitCode = 0
	var n int

	for {
		err = self.readFully(header[0:5])
		if err != nil {
			return
		}

		payloadLength := (int(header[0]) << 24)
		payloadLength |= (int(header[1]) << 16)
		payloadLength |= (int(header[2]) << 8)
		payloadLength |= (int(header[3]) << 0)

		payloadType := header[4]
		//log.Printf(self.output.WriteString())
		//log.Printf("Chunk %v %v %v\n", header, payloadLength, payloadType)
      if self.Output == nil {
			self.Output = os.Stdout
			//log.Printf("Setting StdOUT")
		}
		if self.Outerr == nil {
			self.Outerr = os.Stderr
		}
      
		var dest io.Writer
		switch payloadType {
		case '1':
			dest = self.Output
		case '2':
			dest = self.Outerr

		case 'S':
			// Undocumented chunk type "start input"
			if payloadLength != 0 {
				err = fmt.Errorf("Expected 0 length for S chunk")
				return
			}
			continue

		case 'X':
			err = self.readFully(buffer[0:payloadLength])
			//log.Printf("buffer X " + string(buffer))
			if err != nil {
				return
			}

			exitCode, err = strconv.Atoi(string(buffer[0:payloadLength]))
			if err != nil {
				return
			}

			return exitCode, nil

		default:
			err = fmt.Errorf("Unexpected chunk type %v", payloadType)
			return
		}

		for payloadLength > 0 {
			read := payloadLength
			if read > N {
				read = N
			}
			n, err = self.Conn.Read(buffer[0:read])
			if n > 0 {
				_, writeErr := dest.Write(buffer[0:n])
				if writeErr != nil {
					return 0, writeErr
				}

				payloadLength -= n
			}
         //log.Printf("buffer" + string(buffer))
			if err != nil {
				return
			}

		}
	}

	err = fmt.Errorf("Unreachable response")

	return
}

func (self *NailgunConnection) close() (err error) {
	err = self.Conn.Close()
	if err != nil {
		return
	}
	return nil
}

func findEnvironmentVariable(key string) (value string) {
	prefix := key + "="
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, prefix) {
			value = env[len(prefix):]
			return
		}
	}
	return ""
}


func main() {
	if len(os.Args) <= 1 {
		log.Printf("Must pass (at least) command/class to run")
		os.Exit(2)
	}

	address := findEnvironmentVariable("NAILGUN_SERVER")
	if address == "" {
		address = "127.0.0.1"
	}
	port := findEnvironmentVariable("NAILGUN_PORT")
	if port == "" {
		port = "2113"
	}
  
	
	var err error
	var conn net.Conn
   //log.Printf("connecting to port: %v", port)
	
	for i := 0; i < 10; i++ {
		if strings.HasPrefix(address, "local:") {
			socketFile := strings.Split(address, ":")[1]
			conn, err = net.Dial("unix", socketFile)
		} else {
			conn, err = net.Dial("tcp", address + ":" + port)
		}
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond * 50)
	}

	if err != nil {
		log.Printf("Unable to connect to nailgun server: %s\n", err.Error())
		os.Exit(1)
	}

 	ng := &NailgunConnection{}
	ng.Conn = conn
	
	// we always skip the first two args the first is our name
	// the second is the command to run
	exitCode, err := ng.SendCommand(os.Args[1], os.Args[2:])
	//log.Printf("Exit Code: %d", exitCode)
	if err != nil {
		if err != io.EOF {
			log.Printf("Error communicating with background process: %v\n", err)
			os.Exit(2)
		}
	}

	os.Exit(exitCode)
}
