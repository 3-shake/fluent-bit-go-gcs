package main

import (
	"C"
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/fluent/fluent-bit-go/output"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
)
import "strings"

var (
	gcsClient Client
	err       error
)

//export FLBPluginRegister
func FLBPluginRegister(def unsafe.Pointer) int {
	return output.FLBPluginRegister(def, "gcs", "GCS Output plugin written in GO!")
}

//export FLBPluginInit
func FLBPluginInit(plugin unsafe.Pointer) int {
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", output.FLBPluginConfigKey(plugin, "Credential"))
	gcsClient, err = NewClient()
	if err != nil {
		output.FLBPluginUnregister(plugin)
		log.Fatal(err)
		return output.FLB_ERROR
	}

	// Set the context
	output.FLBPluginSetContext(plugin, map[string]string{
		"region": output.FLBPluginConfigKey(plugin, "Region"),
		"bucket": output.FLBPluginConfigKey(plugin, "Bucket"),
		"prefix": output.FLBPluginConfigKey(plugin, "Prefix"),
	})

	return output.FLB_OK
}

//export FLBPluginFlushCtx
func FLBPluginFlushCtx(ctx, data unsafe.Pointer, length C.int, tag *C.char) int {
	// Type assert context back into the original type for the Go variable
	values := output.FLBPluginGetContext(ctx).(map[string]string)

	log.Printf("[event] Flush called, context %s, %s, %v\n", values["region"], values["bucket"], C.GoString(tag))
	dec := output.NewDecoder(data, int(length))
	var rs []map[interface{}]interface{}

	for {
		ret, ts, record := output.GetRecord(dec)
		if ret != 0 {
			break
		}
		// Get timestamp
		var timestamp time.Time
		switch t := ts.(type) {
		case output.FLBTime:
			timestamp = ts.(output.FLBTime).Time
		case uint64:
			timestamp = time.Unix(int64(t), 0)
		default:
			log.Println("[warn] timestamp isn't known format. Use current time.")
			timestamp = time.Now()
		}
		record["ts"] = timestamp
		rs = append(rs, record)
	}

	if err := SaveRecords(values["bucket"], values["prefix"], C.GoString(tag), rs); err != nil {
		log.Printf("[warn] error sending message in GCS: %v\n", err)
		return output.FLB_RETRY
	}

	// Return options:
	//
	// output.FLB_OK    = data have been processed.
	// output.FLB_ERROR = unrecoverable error, do not try this again.
	// output.FLB_RETRY = retry to flush later
	return output.FLB_OK
}

func SaveRecords(bucket, prefix, tag string, records []map[interface{}]interface{}) error {
	t := time.Now()
	j, err := createJSONLines(records)
	if err != nil {
		return err
	}
	objectKey := GenerateObjectKey(prefix, tag, t)
	if err := gcsClient.Write(bucket, objectKey, bytes.NewReader(j)); err != nil {
		return err
	}
	return nil
}

// GenerateObjectKey : gen format object name PREFIX/date/hour/tag/timestamp_uuid.log
func GenerateObjectKey(prefix, tag string, t time.Time) string {
	fileName := fmt.Sprintf("%s.log", uuid.Must(uuid.NewRandom()).String())
	return filepath.Join(prefix, tag, t.Format("20060102/15"), fileName)
}

func createJSONLines(records []map[interface{}]interface{}) ([]byte, error) {
	rs := make([]string, len(records))
	for i, r := range records {
		j, err := createJSON(r)
		if err != nil {
			return nil, err
		}
		rs[i] = j
	}
	return []byte(strings.Join(rs, "\n")), nil
}

func createJSON(record map[interface{}]interface{}) (string, error) {
	js, err := jsoniter.Marshal(parseMap(record))
	if err != nil {
		return "{}", err
	}

	return string(js), nil
}

func parseMap(mapInterface map[interface{}]interface{}) map[string]interface{} {
	m := make(map[string]interface{})

	for k, v := range mapInterface {
		switch t := v.(type) {
		case []byte:
			// prevent encoding to base64
			m[k.(string)] = string(t)
		case map[interface{}]interface{}:
			m[k.(string)] = parseMap(t)
		default:
			m[k.(string)] = v
		}
	}

	return m
}

//export FLBPluginExit
func FLBPluginExit() int {
	return output.FLB_OK
}

func main() {}
