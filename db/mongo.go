package db

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Account đại diện cho 1 Apple ID đã đăng nhập của user.
type Account struct {
	AppleID  string `bson:"appleId"`
	Password string `bson:"password"`
	AddedAt  int64  `bson:"addedAt"`
}

type User struct {
	ID            int64      `bson:"userId"`
	FirstName     string     `bson:"firstName"`
	Username      string     `bson:"username"`
	IsPremium     bool       `bson:"isPremium"`
	PremiumExpiry *time.Time `bson:"premiumExpiry,omitempty"`
	UsageCount    int        `bson:"usageCount"`
	LastUsed      int64      `bson:"lastUsed"`
	CreatedAt     int64      `bson:"createdAt"`
	// Active account (backward compat — luôn trùng với 1 entry trong Accounts khi multi-account)
	AppleID      string `bson:"appleId,omitempty"`
	Password     string `bson:"password,omitempty"`
	LastDownload int64  `bson:"lastDownload,omitempty"`
	LoginStep    string `bson:"loginStep,omitempty"`
	TempAppleID  string `bson:"tempAppleId,omitempty"`
	// Multi-account support
	Accounts []Account `bson:"accounts,omitempty"`
}

type MongoDB struct {
	Client *mongo.Client
	Users  *mongo.Collection
}

func Connect(uri string) (*MongoDB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, err
	}

	db := client.Database("ipa-downloader-bot-new")
	users := db.Collection("users")

	// Create indexes
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = users.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    bson.M{"userId": 1},
			Options: options.Index().SetUnique(true),
		})
	}()

	return &MongoDB{
		Client: client,
		Users:  users,
	}, nil
}

func (m *MongoDB) GetUser(ctx context.Context, userID int64) (*User, error) {
	var user User
	err := m.Users.FindOne(ctx, bson.M{"userId": userID}).Decode(&user)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	return &user, err
}
func (m *MongoDB) GetAllUsers(ctx context.Context) ([]User, error) {
	cursor, err := m.Users.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (m *MongoDB) GetActiveSessionUsers(ctx context.Context, timeout int64) ([]User, error) {
	now := time.Now().UnixMilli()
	cursor, err := m.Users.Find(ctx, bson.M{
		"loginStep": bson.M{"$ne": ""},
		"lastUsed":  bson.M{"$lt": now - timeout},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (m *MongoDB) GetPremiumUsers(ctx context.Context) ([]User, error) {
	cursor, err := m.Users.Find(ctx, bson.M{"isPremium": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var users []User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func (m *MongoDB) SaveUser(ctx context.Context, user *User) error {
	opts := options.Update().SetUpsert(true)
	_, err := m.Users.UpdateOne(ctx, bson.M{"userId": user.ID}, bson.M{"$set": user}, opts)
	return err
}

func (m *MongoDB) UpdateUser(ctx context.Context, userID int64, update bson.M) error {
	_, err := m.Users.UpdateOne(ctx, bson.M{"userId": userID}, update)
	return err
}

func (m *MongoDB) UnsetFields(ctx context.Context, userID int64, fields bson.M) error {
	_, err := m.Users.UpdateOne(ctx, bson.M{"userId": userID}, bson.M{"$unset": fields})
	return err
}
