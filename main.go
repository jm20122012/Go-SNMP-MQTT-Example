package main

import (
	"log"
	"fmt"
	"time"
	"sync"
	"os"
	"strconv"
	"math/big"
	"encoding/json"
	"github.com/joho/godotenv"
	"github.com/gosnmp/gosnmp"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type PfsenseData struct {
	Intf1	string
	Intf2	string
	Intf3	string
}

type AvtechData struct {
	Celcius 	*big.Int
	Fahrenheit	*big.Int
	Label		string
}

func parsePfsenseSnmp(snmpData *gosnmp.SnmpPacket) string {
	var data PfsenseData = PfsenseData {
		Intf1: string(snmpData.Variables[0].Value.([]byte)),
		Intf2: string(snmpData.Variables[1].Value.([]byte)),
		Intf3: string(snmpData.Variables[2].Value.([]byte)),
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Println("Error marshaling JSON object: ", err)
	}
	jsonString := string(jsonBytes)

	return jsonString
}

func parseAvtechData(snmpData *gosnmp.SnmpPacket) string {
	var data AvtechData = AvtechData{
		Celcius: gosnmp.ToBigInt(snmpData.Variables[0].Value),
		Fahrenheit: gosnmp.ToBigInt(snmpData.Variables[1].Value),
		Label: string(snmpData.Variables[2].Value.([]byte)),
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Println("Error marshaling JSON object: ", err)
	}
	jsonString := string(jsonBytes)

	return jsonString
}

func snmpWorker (snmpObj *gosnmp.GoSNMP, targetName string, oids []string, wg *sync.WaitGroup, mqttOpts *mqtt.ClientOptions) {
	
	defer wg.Done()

	log.Println("Inside snmpWorker for ", targetName)
	log.Println("MQTT Options: ", mqttOpts)

	// Create MQTT client object
	mqttClient := mqtt.NewClient(mqttOpts)

	// Connect to MQTT broker
	log.Println("Connecting to MQTT broker...")
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	log.Println("Connecting to target: ", targetName)
	
	err := snmpObj.Connect()
	
	if err != nil {
		log.Fatalf("Connect() err: %v", err)
		return
	} else {
		log.Printf("Connection: %s", snmpObj.Conn)
		log.Println("Connection succesful")
	}

	for true {
		result, err := snmpObj.Get(oids)
		if err != nil {
			log.Println("Error getting SNMP results: ", err)
		}
	
		for index, value := range result.Variables {
			// log.Println("Results: ")
			// log.Printf("Device: %s\n Index: %d\n Name: %s\n Value: %s\n", targetName, index, value)

			switch value.Type {
			case gosnmp.OctetString:
				log.Printf("Device: %s, Index: %d, Value: %s", targetName, index, string(value.Value.([]byte)))
			
			case gosnmp.Integer:
				log.Printf("Device: %s, Index: %d, Value: %s", targetName, index, gosnmp.ToBigInt(value.Value))
			}
		}

		if targetName == "pfSense" {
			parsed := parsePfsenseSnmp(result)
			publish(mqttClient, parsed)
		}

		if targetName == "avtech" {
			parsed := parseAvtechData(result)
			publish(mqttClient, parsed)
		}


		time.Sleep(50 * time.Millisecond) // Delay one second
	}
}

var connectHandler mqtt.OnConnectHandler = func(client mqtt.Client) {
    log.Println("Connected")
}

var connectLostHandler mqtt.ConnectionLostHandler = func(client mqtt.Client, err error) {
    log.Printf("Connect lost: %v", err)
}

func publish (client mqtt.Client, msg string) {
	log.Println("Publishing message ", msg)
	token := client.Publish("test", 0, false, msg)
	log.Println("Message published, waiting on token...")
	token.Wait()
}

func main() {
	// Load environment variables
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading environment variables: ", err)
	} else {
		log.Println("Environment variables loaded")
	}

	// Constant MQTT options
	var broker = os.Getenv("MQTT_BROKER_IP")
	port, err := strconv.Atoi(os.Getenv("MQTT_BROKER_PORT"))
	if err != nil {
		log.Println("Error importing mqtt port")
	}

	// --------------------------- pfSense Setup --------------------------- //
	// Configure pfsense MQTT options
	pfsenseMqttOpts := mqtt.NewClientOptions()
	pfsenseMqttOpts.AddBroker(fmt.Sprintf("tcp://%s:%d", broker, port))
	pfsenseMqttOpts.SetClientID("pfsense_mqtt_client")
	pfsenseMqttOpts.SetOrderMatters(false)
	// pfsenseMqttOpts.SetUsername(os.Getenv("MQTT_USERNAME"))
	// pfsenseMqttOpts.SetPassword(os.Getenv("MQTT_PW"))
    pfsenseMqttOpts.OnConnect = connectHandler
    pfsenseMqttOpts.OnConnectionLost = connectLostHandler

	// log.Println("Pfsense MQTT brokers: ", pfsenseMqttOpts.Servers)

	// Setup task group config
	taskCount := 2
	var wg sync.WaitGroup
	wg.Add(taskCount) // Add n tasks to wait group

	// Setup for pfSense SNMP
	var pfsenseOids []string = []string {
		"1.3.6.1.4.1.12325.1.200.1.8.2.1.2.1",
		"1.3.6.1.4.1.12325.1.200.1.8.2.1.2.2",
		"1.3.6.1.4.1.12325.1.200.1.8.2.1.2.3",
	}

	var pfsenseSnmp gosnmp.GoSNMP
	pfsenseSnmp.Target = os.Getenv("PFSENSE_IP")
	pfsenseSnmp.Version = gosnmp.Version2c
	pfsenseSnmp.Community = "public"
	pfsenseSnmp.Port = 161
	pfsenseSnmp.Timeout = time.Duration(2) * time.Second
	pfsenseSnmp.Retries = 3

	go snmpWorker(&pfsenseSnmp, "pfSense", pfsenseOids, &wg, pfsenseMqttOpts)
	// go pfsenseWorker(&wg)

	// --------------------------- Avtech Setup --------------------------- //
	// Configure avtech MQTT options
	avtechMqttOpts := mqtt.NewClientOptions()
	avtechMqttOpts.AddBroker(fmt.Sprintf("tcp://%s:%d", broker, port))
	avtechMqttOpts.SetClientID("avtech_mqtt_client")
	avtechMqttOpts.SetOrderMatters(false)
	// avtechMqttOpts.SetUsername(os.Getenv("MQTT_USERNAME"))
	// avtechMqttOpts.SetPassword(os.Getenv("MQTT_PW"))
    avtechMqttOpts.OnConnect = connectHandler
    avtechMqttOpts.OnConnectionLost = connectLostHandler

	// Setup for Avtech temp sensor
	var avtechOids []string = []string {
		"1.3.6.1.4.1.20916.1.9.1.1.1.1.0",
		"1.3.6.1.4.1.20916.1.9.1.1.1.2.0",
		"1.3.6.1.4.1.20916.1.9.1.1.1.3.0",
	}

	var avtechSnmp gosnmp.GoSNMP
	avtechSnmp.Target = os.Getenv("AVTECH_IP")
	avtechSnmp.Version = gosnmp.Version1
	avtechSnmp.Community = "public"
	avtechSnmp.Port = 161
	avtechSnmp.Timeout = time.Duration(2) * time.Second
	avtechSnmp.Retries = 3

	go snmpWorker(&avtechSnmp, "avtech", avtechOids, &wg, avtechMqttOpts)

	wg.Wait() // Wait for all tasks to finish


}
